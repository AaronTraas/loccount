// SPDX-License-Identifier: BSD-2-Clause
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"sync"
)

const version string = "2.0"

/*
How to add support for a language to this program:

All the language-specific information this program needs to know to do
its job is the syntax of comments and string literals.  Generally,
languages fall into one of the following groups:

* C-like: C is the prototype. This code recognizes them by file
  extension and verifier.  These languages have two kinds of comment.
  One is a block comment delimited by two distinct strings and the
  second is a winged comment introduced by a third string and
  terminated by newline.  The following bool signals whether newlines
  are permitted in strings.  You can add support simply by appending
  an initializer to the genericLanguages table; any entry with a
  nonempty comment leader invokes C-like parsing.

* Generic languages have only winged comments, usually led with #.
  This code recognizes them by file extension and verifier.  You can
  append an initializer to the genericLanguages table specifying a
  name, an extension, and the winged-comment leader.  Any entry with
  empty commentleader and trailer strings gets generic parsing.

* Scripting languages have only winged comments, always led with #.
  This code recognizes them by file extension, or by looking for a
  hashbang line identifying the interpreter.  You can append an
  initializer to the scriptingLanguages table specifying a name, an
  extension, and a matching string to look for in a hashbang line.

* Pascal-likes use the (* *) block comment syntax.  This code
  recognizes them by file extension and verifier.  You can append an
  initializer to the PascalLikes table specifying a name, an
  extension, and a boolean saying whether the language uses { } as
  additional pair of block comments.

* Fortran-likes use various start-of-line characters as comment
  leaders.  This code recognizes them by file extension only.  You can
  append an initializer to the fortranLikes table specifying a pair of
  (compiled) regular expressions; comments are recognized by matching
  the first and not the second.

You may add multiple entries with the same language name, but extensions
must be unique across all tables - *except* that entries with verifiers
may share extensions with each other and with one trailing entry that has
no verifier.
*/

// Following code swiped from Michael T. Jones's "walk" package.
// It's a parallelized implementation of tree-walking that's
// faster than the version in the system filepath library.
// Note, however, it seems to have a limitation - does not like paths
// containing "..".

type visitData struct {
	path string
	info os.FileInfo
}

// WalkFunc is the type of the function called for each file or directory
// visited by Walk. The path argument contains the argument to Walk as a
// prefix; that is, if Walk is called with "dir", which is a directory
// containing the file "a", the walk function will be called with argument
// "dir/a". The info argument is the os.FileInfo for the named path.
//
// If there was a problem walking to the file or directory named by path, the
// incoming error will describe the problem and the function can decide how
// to handle that error (and Walk will not descend into that directory). If
// an error is returned, processing stops. The sole exception is that if path
// is a directory and the function returns the special value SkipDir, the
// contents of the directory are skipped and processing continues as usual on
// the next file.
type WalkFunc func(path string, info os.FileInfo, err error) error

type walkState struct {
	walkFn     WalkFunc
	v          chan visitData // files to be processed
	active     sync.WaitGroup // number of files to process
	lock       sync.RWMutex
	firstError error // accessed using lock
}

func (ws *walkState) terminated() bool {
	ws.lock.RLock()
	done := ws.firstError != nil
	ws.lock.RUnlock()
	return done
}

func (ws *walkState) setTerminated(err error) {
	ws.lock.Lock()
	if ws.firstError == nil {
		ws.firstError = err
	}
	ws.lock.Unlock()
	return
}

func (ws *walkState) visitChannel() {
	for file := range ws.v {
		ws.visitFile(file)
		ws.active.Add(-1)
	}
}

// readDirNames reads the directory named by dirname and returns
// a sorted list of directory entries.
func readDirNames(dirname string) ([]string, error) {
	f, err := os.Open(dirname)
	if err != nil {
		return nil, err
	}
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return nil, err
	}
	sort.Strings(names) // omit sort to save 1-2%
	return names, nil
}

func (ws *walkState) visitFile(file visitData) {
	if ws.terminated() {
		return
	}

	err := ws.walkFn(file.path, file.info, nil)
	if err != nil {
		if !(file.info.IsDir() && err == filepath.SkipDir) {
			ws.setTerminated(err)
		}
		return
	}

	if !file.info.IsDir() {
		return
	}

	names, err := readDirNames(file.path)
	if err != nil {
		err = ws.walkFn(file.path, file.info, err)
		if err != nil {
			ws.setTerminated(err)
		}
		return
	}

	here := file.path
	for _, name := range names {
		file.path = filepath.Join(here, name)
		file.info, err = os.Lstat(file.path)
		if err != nil {
			err = ws.walkFn(file.path, file.info, err)
			if err != nil && (!file.info.IsDir() || err != filepath.SkipDir) {
				ws.setTerminated(err)
				return
			}
		} else {
			switch file.info.IsDir() {
			case true:
				ws.active.Add(1) // presume channel send will succeed
				select {
				case ws.v <- file:
					// push directory info to queue for concurrent traversal
				default:
					// undo increment when send fails and handle now
					ws.active.Add(-1)
					ws.visitFile(file)
				}
			case false:
				err = ws.walkFn(file.path, file.info, nil)
				if err != nil {
					ws.setTerminated(err)
					return
				}
			}
		}
	}
}

// Walk walks the file tree rooted at root, calling walkFn for each file or
// directory in the tree, including root. All errors that arise visiting files
// and directories are filtered by walkFn. The files are walked in a random
// order. walk does not follow symbolic links.

func walk(root string, walkFn WalkFunc) error {
	info, err := os.Lstat(root)
	if err != nil {
		return walkFn(root, nil, err)
	}

	ws := &walkState{
		walkFn: walkFn,
		v:      make(chan visitData, 1024),
	}
	defer close(ws.v)

	ws.active.Add(1)
	ws.v <- visitData{root, info}

	walkers := 16
	for i := 0; i < walkers; i++ {
		go ws.visitChannel()
	}
	ws.active.Wait()

	return ws.firstError
}

// Swiped code ends here

// SourceStat - line count record for a specified path
type SourceStat struct {
	Path     string
	Language string
	SLOC     uint
	LLOC     uint
}

var debug int
var exclusions *regexp.Regexp
var pipeline chan SourceStat

// Data tables driving the recognition and counting of classes of languages.

type genericLanguage struct {
	name           string
	suffix         string
	commentleader  string
	commenttrailer string
	eolcomment     string
	multistring    string
	flags          uint
	terminator     string
	verifier       func(*countContext, string) bool
}

var genericLanguages []genericLanguage

type scriptingLanguage struct {
	name     string
	suffix   string
	hashbang string
	verifier func(*countContext, string) bool
}

var scriptingLanguages []scriptingLanguage

type pascalLike struct {
	name            string
	suffix          string
	bracketcomments bool
	terminator      string
	verifier        func(*countContext, string) bool
}

var pascalLikes []pascalLike

const dt = `"""`
const st = `'''`

var dtriple, striple, dtrailer, strailer, dlonely, slonely *regexp.Regexp

var podheader *regexp.Regexp

type fortranLike struct {
	name      string
	suffix    string
	comment   *regexp.Regexp
	nocomment *regexp.Regexp
}

var fortranLikes []fortranLike

var neverInterestingByPrefix []string
var neverInterestingByInfix []string
var neverInterestingBySuffix map[string]bool
var neverInterestingByBasename map[string]bool

var cHeaderPriority []string
var generated string

// Syntax flags
const nf	= 0x00	// no flags
const eolwarn	= 0x01	// Warn on EOL in string
const cbs	= 0x02	// C-style backslash escapes
const gotick	= 0x04	// Strong backtick a la Go

func init() {
	// For speed, try to put more common languages and extensions
	// earlier in this list.
	//
	// Verifiers are expensive, so try to put extensions that need them
	// after extensions that don't. But remember that you don't pay the
	// overhead for a verifier unless the extension matches.
	//
	// If you have multiple entries for an extension, (a) all the entries
	// with verifiers should go first, and (b) there should be at most one
	// entry without a verifier (because any second and later ones will be
	// pre-empted by it).
	//
	// All entries for a given language should be in a contiguous span,
	// otherwise the primitive duplicate director in listLanguages will
	// be foiled.
	//
	// See https://en.wikipedia.org/wiki/Comparison_of_programming_languages_(syntax)
	genericLanguages = []genericLanguage{
		/* C family */
		{"c", ".c", "/*", "*/", "//", "", eolwarn|cbs, ";", nil},
		{"c-header", ".h", "/*", "*/", "//", "", eolwarn|cbs, ";", nil},
		{"c-header", ".hpp", "/*", "*/", "//", "", eolwarn|cbs, ";", nil},
		{"c-header", ".hxx", "/*", "*/", "//", "", eolwarn|cbs, ";", nil},
		{"yacc", ".y", "/*", "*/", "//", "", eolwarn|cbs, ";", nil},
		{"lex", ".l", "/*", "*/", "//", "", eolwarn|cbs, ";", reallyLex},
		{"c++", ".cpp", "/*", "*/", "//", "", eolwarn|cbs, ";", nil},
		{"c++", ".cxx", "/*", "*/", "//", "", eolwarn|cbs, ";", nil},
		{"c++", ".cc", "/*", "*/", "//", "", eolwarn|cbs, ";", nil},
		{"java", ".java", "/*", "*/", "//", "", eolwarn|cbs, ";", nil},
		{"javascript", ".js", "/*", "*/", "//", "", eolwarn|cbs, "", nil},
		{"obj-c", ".m", "/*", "*/", "//", "", eolwarn|cbs, ";", reallyObjectiveC},
		{"c#", ".cs", "/*", "*/", "//", "", eolwarn|cbs, ";", nil},
		//{"html", ".html", "<!--", "-->", "", "", nf, "", nil},
		//{"html", ".htm", "<!--", "-->", "", "", nf, "", nil},
		//{"xml", ".xml", "<!--", "-->", "", "", nf, "", nil},
		{"php", ".php", "/*", "*/", "//", "", eolwarn|cbs, ";", nil},
		{"php3", ".php", "/*", "*/", "//", "", eolwarn|cbs, ";", nil},
		{"php4", ".php", "/*", "*/", "//", "", eolwarn|cbs, ";", nil},
		{"php5", ".php", "/*", "*/", "//", "", eolwarn|cbs, ";", nil},
		{"php6", ".php", "/*", "*/", "//", "", eolwarn|cbs, ";", nil},
		{"php7", ".php", "/*", "*/", "//", "", eolwarn|cbs, ";", nil},
		{"go", ".go", "/*", "*/", "//", "`", eolwarn|cbs|gotick, "", nil},
		{"swift", ".swift", "/*", "*/", "//", "", eolwarn, "", nil},
		{"sql", ".sql", "/*", "*/", "--", "", nf, "", nil},
		{"haskell", ".hs", "{-", "-}", "--", "", eolwarn, "", nil},
		{"pl/1", ".pl1", "/*", "*/", "", "", eolwarn, ";", nil},
		/* everything else */
		{"asm", ".asm", "/*", "*/", ";", "", eolwarn, "\n", nil},
		{"asm", ".s", "/*", "*/", ";", "", eolwarn, "\n", nil},
		{"asm", ".S", "/*", "*/", ";", "", eolwarn, "\n", nil},
		{"ada", ".ada", "", "", "--", "", eolwarn, ";", nil},
		{"ada", ".adb", "", "", "--", "", eolwarn, ";", nil},
		{"ada", ".ads", "", "", "--", "", eolwarn, ";", nil},
		{"ada", ".pad", "", "", "--", "", eolwarn, "", nil}, // Oracle Ada preprocessoer.
		{"css", ".css", "/*", "*/", "", "", eolwarn, "", nil},
		{"makefile", ".mk", "", "", "#", "", eolwarn, "", nil},
		{"makefile", "Makefile", "", "", "#", "", eolwarn, "", nil},
		{"makefile", "makefile", "", "", "#", "", eolwarn, "", nil},
		{"makefile", "Imakefile", "", "", "#", "", eolwarn, "", nil},
		{"m4", ".m4", "", "", "#", "", eolwarn, "", nil},
		{"lisp", ".lisp", "", "", ";", "", eolwarn, "", nil},
		{"lisp", ".lsp", "", "", ";", "", eolwarn, "", nil}, // XLISP
		{"lisp", ".cl", "", "", ";", "", eolwarn, "", nil},  // Common Lisp
		{"lisp", ".l", "", "", ";", "", eolwarn, "", nil},
		{"scheme", ".scm", "", "", ";", "", eolwarn, "", nil},
		{"elisp", ".el", "", "", ";", "", eolwarn, "", nil},    // Emacs Lisp
		{"clojure", ".clj", "", "", ";", "", eolwarn, "", nil}, // Clojure
		{"clojure", ".cljc", "", "", ";", "", eolwarn, "", nil},
		{"clojurescript", ".cljs", "", "", ";", "", eolwarn, "", nil},
		{"cobol", ".CBL", "", "", "*", "", eolwarn, "", nil},
		{"cobol", ".cbl", "", "", "*", "", eolwarn, "", nil},
		{"cobol", ".COB", "", "", "*", "", eolwarn, "", nil},
		{"cobol", ".cob", "", "", "*", "", eolwarn, "", nil},
		{"eiffel", ".e", "", "", "--", "", eolwarn, "", nil},
		{"sather", ".sa", "", "", "--", "", eolwarn, ";", reallySather},
		{"lua", ".lua", "--[[", "]]", "--", "", eolwarn, "", nil},
		{"clu", ".clu", "", "", "%", "", eolwarn, ";", nil},
		{"rust", ".rs", "", "", "//", "", eolwarn, ";", nil},
		{"rust", ".rlib", "", "", "//", "", eolwarn, ";", nil},
		{"erlang", ".erl", "", "", "%", "", eolwarn, "", nil},
		{"vhdl", ".vhdl", "", "", "--", "", nf, "", nil},
		{"verilog", ".v", "/*", "*/", "//", "", eolwarn, ";", nil},
		{"verilog", ".vh", "/*", "*/", "//", "", eolwarn, ";", nil},
		//{"turing", ".t", "", "", "%", "", eolwarn, "", nil},
		{"d", ".d", "/+", "+/", "//", "", eolwarn, ";", nil},
		{"occam", ".f", "", "", "//", "", eolwarn, "", reallyOccam},
		{"f#", ".fs", "", "", "//", "", eolwarn, "", nil},
		{"f#", ".fsi", "", "", "//", "", eolwarn, "", nil},
		{"f#", ".fsx", "", "", "//", "", eolwarn, "", nil},
		{"f#", ".fscript", "", "", "//", "", eolwarn, "", nil},
		{"kotlin", ".kt", "", "", "//", "", eolwarn, "", nil},
		{"dart", ".dart", "", "", "//", "", eolwarn, ";", nil},
		{"prolog", ".pl", "", "", "%", "", eolwarn, ".", reallyProlog},
		{"mumps", ".m", "", "", ";", "", eolwarn, "", nil},
		{"mumps", ".mps", "", "", ";", "", eolwarn, "", nil},
		{"mumps", ".m", "", "", ";", "", eolwarn, "", nil},
		{"pop11", ".p", "", "", ";", "", eolwarn, "", reallyPOP11},
		{"rebol", ".r", "", "", "comment", "", nf, "", nil},
		{"simula", ".sim", "", "", "comment", "", nf, ";", nil},
		{"icon", ".icn", "", "", "#", "", nf, "", nil},
		{"algol60", ".alg", "", "", "COMMENT", "", nf, ";", nil},
		// autoconf cruft
		{"autotools", "config.h.in", "/*", "*/", "//", "", eolwarn, "", nil},
		{"autotools", "autogen.sh", "", "", "#", "", eolwarn, "", nil},
		{"autotools", "configure.in", "", "", "#", "", eolwarn, "", nil},
		{"autotools", "Makefile.in", "", "", "#", "", eolwarn, "", nil},
		{"autotools", ".am", "", "", "#", "", eolwarn, "", nil},
		{"autotools", ".ac", "", "", "#", "", eolwarn, "", nil},
		{"autotools", ".mf", "", "", "#", "", eolwarn, "", nil},
		// Scons
		{"scons", "SConstruct", "", "", "#", "", eolwarn, "", nil},
	}

	var err error
	dtriple, err = regexp.Compile(dt + "." + dt)
	if err != nil {
		panic(err)
	}
	striple, err = regexp.Compile(st + "." + st)
	if err != nil {
		panic(err)
	}
	dlonely, err = regexp.Compile("^[ \t]*\"[^\"]+\"")
	if err != nil {
		panic(err)
	}
	slonely, err = regexp.Compile("^[ \t]*'[^']+'")
	if err != nil {
		panic(err)
	}
	strailer, err = regexp.Compile(".*" + st)
	if err != nil {
		panic(err)
	}
	dtrailer, err = regexp.Compile(".*" + dt)
	if err != nil {
		panic(err)
	}

	scriptingLanguages = []scriptingLanguage{
		{"tcl", ".tcl", "tcl", nil}, /* before sh, because tclsh */
		{"tcl", ".tcl", "wish", nil},
		{"csh", ".csh", "csh", nil},
		{"shell", ".sh", "sh", nil},
		{"ruby", ".rb", "ruby", nil},
		{"awk", ".awk", "awk", nil},
		{"sed", ".sed", "sed", nil},
		{"expect", ".exp", "expect", reallyExpect},
	}
	pascalLikes = []pascalLike{
		{"pascal", ".pas", true, ";", nil},
		{"pascal", ".p", true, ";", reallyPascal},
		{"pascal", ".inc", true, ";", reallyPascal},
		{"modula3", ".i3", false, ";", nil},
		{"modula3", ".m3", false, ";", nil},
		{"modula3", ".ig", false, ";", nil},
		{"modula3", ".mg", false, ";", nil},
		{"ml", ".ml", false, "", nil}, // Could be CAML or OCAML
		{"mli", ".ml", false, "", nil},
		{"mll", ".ml", false, "", nil},
		{"mly", ".ml", false, "", nil},
		{"oberon", ".mod", false, ";", nil},
	}

	var ferr error
	f90comment, ferr := regexp.Compile("^([ \t]*!|[ \t]*$)")
	if ferr != nil {
		panic("unexpected failure while building f90 comment analyzer")
	}
	f90nocomment, ferr := regexp.Compile("^[ \t]*!(hpf|omp)[$]")
	if ferr != nil {
		panic("unexpected failure while building f90 no-comment analyzer")
	}
	f77comment, ferr := regexp.Compile("^([c*!]|[ \t]+!|[ \t]*$)")
	if ferr != nil {
		panic("unexpected failure while building f77 comment analyzer")
	}
	f77nocomment, ferr := regexp.Compile("^[c*!](hpf|omp)[$]")
	if ferr != nil {
		panic("unexpected failure while building f77 nocomment analyzer")
	}
	fortranLikes = []fortranLike{
		{"fortran90", ".f90", f90comment, f90nocomment},
		{"fortran95", ".f95", f90comment, f90nocomment},
		{"fortran03", ".f03", f90comment, f90nocomment},
		{"fortran", ".f77", f77comment, f77nocomment},
		{"fortran", ".f", f77comment, f77nocomment},
	}

	var perr error
	podheader, perr = regexp.Compile("^=[a-zA-Z]")
	if perr != nil {
		panic(perr)
	}

	neverInterestingByPrefix = []string{"."}
	neverInterestingByInfix = []string{".so.", "/."}
	ignoreSuffixes := []string{"~",
		".a", ".la", ".o", ".so", ".ko",
		".gif", ".jpg", ".jpeg", ".ico", ".xpm", ".xbm", ".bmp",
		".ps", ".pdf", ".eps",
		".tfm", ".ttf", ".bdf", ".afm",
		".fig", ".pic",
		".pyc", ".pyo", ".elc",
		".1", ".2", ".3", ".4", ".5", ".6", ".7", ".8", ".n", ".man",
		".html", ".htm", ".sgml", ".xml",
		".adoc", "md", ".txt", ".tex", ".texi",
		".po",
		".gz", ".bz2", ".Z", ".tgz", ".zip",
		".au", ".wav", ".ogg",
	}
	neverInterestingBySuffix = make(map[string]bool)
	for i := range ignoreSuffixes {
		neverInterestingBySuffix[ignoreSuffixes[i]] = true
	}
	neverInterestingByBasename = map[string]bool{
		"readme": true, "readme.tk": true, "readme.md": true,
		"changelog": true, "repository": true, "changes": true,
		"bugs": true, "todo": true, "copying": true, "maintainers": true,
		"news":      true,
		"configure": true, "autom4te.cache": true, "config.log": true,
		"config.status": true,
		"lex.yy.c":      true, "lex.yy.cc": true,
		"y.code.c": true, "y.tab.c": true, "y.tab.h": true,
	}
	cHeaderPriority = []string{"c", "c++", "obj-c"}

	generated = "automatically generated|generated automatically|generated by|a lexical scanner generated by flex|this is a generated file|generated with the.*utility|do not edit|do not hand-hack"

}

// Generic machinery for walking source text to count lines

const stateNORMAL = 0        // in running text
const stateINSTRING = 1      // in single-line string
const stateINMULTISTRING = 2 // in multi-line string
const stateINCOMMENT = 3     // in comment

type countContext struct {
	line             []byte
	lineNumber       uint
	nonblank         bool // Is current line nonblank?
	lexfile          bool // Do we see lex directives?
	wasNewline       bool // Was the last character seen a newline?
	underlyingStream *os.File
	rc               *bufio.Reader
}

func (ctx *countContext) setup(path string) bool {
	var err error
	ctx.underlyingStream, err = os.Open(path)
	if err != nil {
		log.Println(err)
		return false
	}
	ctx.rc = bufio.NewReader(ctx.underlyingStream)
	ctx.lineNumber = 1
	return true
}

func (ctx *countContext) teardown() {
	ctx.underlyingStream.Close()
}

// consume - conditionally consume an expected byte sequence
func (ctx *countContext) consume(expect []byte) bool {
	s, err := ctx.rc.Peek(len(expect))
	if err == nil && bytes.Equal(s, expect) {
		ctx.rc.Discard(len(expect))
		return true
	}
	return false
}

func (ctx *countContext) ispeek(c byte) bool {
	if s, err := ctx.rc.Peek(1); err == nil && s[0] == c {
		return true
	}
	return false
}

// getachar - Get one character, tracking line number
func (ctx *countContext) getachar() (byte, error) {
	c, err := ctx.rc.ReadByte()
	if err != nil && err != io.EOF {
		panic("error while reading a character")
	}
	if ctx.wasNewline {
		ctx.lineNumber++
	}
	if c == '\n' {
		ctx.wasNewline = true
	} else {
		ctx.wasNewline = false
	}
	return c, err
}

// Consume the remainder of a line, updating the line counter
func (ctx *countContext) munchline() bool {
	line, err := ctx.rc.ReadBytes('\n')
	if err == nil {
		ctx.lineNumber++
		ctx.line = line
		return true
	} else if err == io.EOF {
		return false
	} else {
		panic(err)
	}
}

// Consume the remainder of a line, updating the line counter
func (ctx *countContext) drop(excise string) bool {
	cre, err := regexp.Compile(excise)
	if err != nil {
		panic(fmt.Sprintf("unexpected failure %s while compiling %s", err, excise))
	}
	return cre.ReplaceAllLiteral(ctx.line, []byte("")) != nil
}

// matchline - does a given regexp match the last line read?
func (ctx *countContext) matchline(re string) bool {
	cre, err := regexp.Compile(re)
	if err != nil {
		panic(fmt.Sprintf("unexpected failure %s while compiling %s", err, re))
	}
	return cre.Find(ctx.line) != nil
}

func isspace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f'
}

// Verifier functions for checking that files with disputed extensions
// are actually of the types we think they are.

// reallyObjectiveC - returns TRUE if filename contents really are objective-C.
func reallyObjectiveC(ctx *countContext, path string) bool {
	special := false // Did we find a special Objective-C pattern?
	isObjC := false  // Value to determine.
	braceLines := 0  // Lines that begin/end with curly braces.
	plusMinus := 0   // Lines that begin with + or -.
	wordMain := 0    // Did we find "main("?

	ctx.setup(path)
	defer ctx.teardown()

	for ctx.munchline() {
		if ctx.matchline("^\\s*[{}]") || ctx.matchline("[{}];?\\s*") {
			braceLines++
		}
		if ctx.matchline("^\\s*[+-]") {
			plusMinus++
		}
		if ctx.matchline("\\bmain\\s*\\(") { // "main" followed by "("?
			wordMain++
		}
		// Handle /usr/src/redhat/BUILD/egcs-1.1.2/gcc/objc/linking.m:
		if ctx.matchline("(?i)^\\s*\\[object name\\];\\s*") {
			special = true
		}

		if (braceLines > 1) && ((plusMinus > 1) || wordMain > 0 || special) {
			isObjC = true
		}

	}

	if debug > 0 {
		fmt.Fprint(os.Stderr, "objc verifier returned %t on %s\n", isObjC, path)
	}

	return isObjC
}

func hasKeywords(ctx *countContext, path string, lang string, tells []string) bool {
	matching := false // Value to determine.

	ctx.setup(path)
	defer ctx.teardown()

	for ctx.munchline() {
		for i := range tells {
			if ctx.matchline(tells[i]) {
				matching = true
				break
			}
		}
	}

	if debug > 0 {
		fmt.Fprint(os.Stderr, "%s verifier returned %t on %s\n",
			lang, matching, path)
	}

	return matching
}

// reallyOccam - returns TRUE if filename contents really are occam.
func reallyOccam(ctx *countContext, path string) bool {
	return hasKeywords(ctx, path, "occam", []string{"--", "PROC"})
}

// reallyLex - returns TRUE if filename contents really are lex.
func reallyLex(ctx *countContext, path string) bool {
	return hasKeywords(ctx, path, "lex", []string{"%{", "%%", "%}"})
}

// reallyPOP11 - returns TRUE if filename contents really are pop11.
func reallyPOP11(ctx *countContext, path string) bool {
	return hasKeywords(ctx, path, "pop11", []string{"define", "printf"})
}

// reallySather - returns TRUE if filename contents really are sather.
func reallySather(ctx *countContext, path string) bool {
	return hasKeywords(ctx, path, "sather", []string{"class"})
}

// reallyProlog - returns TRUE if filename contents really are prolog.
// Without this check, Perl files will be falsely identified.
func reallyProlog(ctx *countContext, path string) bool {
	ctx.setup(path)
	defer ctx.teardown()

	for ctx.munchline() {
		if bytes.HasPrefix(ctx.line, []byte("#")) {
			return false
		} else if ctx.matchline("\\$[[:alpha]]") {
			return false
		}
	}

	return true
}

// reallyExpect - filename, returns true if its contents really are Expect.
//
// dwheeler had this to say:
//
// Many "exp" files (such as in Apache and Mesa) are just "export" data,
// summarizing something else (e.g., its interface).
// Sometimes (like in RPM) it's just misc. data.
// Thus, we need to look at the file to determine
// if it's really an "expect" file.
// The heuristic is as follows: it's Expect _IF_ it:
// 1. has "load_lib" command and either "#" comments or {}.
// 2. {, }, and one of: proc, if, [...], expect
func reallyExpect(ctx *countContext, path string) bool {
	var isExpect = false // Value to determine.

	var beginBrace bool // Lines that begin with curly braces.
	var endBrace bool   // Lines that begin with curly braces.
	var loadLib bool    // Lines with the LoadLib command.
	var foundProc bool
	var foundIf bool
	var foundBrackets bool
	var foundExpect bool
	var foundPound bool

	ctx.setup(path)
	defer ctx.teardown()

	for ctx.munchline() {
		if ctx.matchline("#") {
			foundPound = true
			// Delete trailing comments
			i := bytes.Index(ctx.line, []byte("#"))
			if i > -1 {
				ctx.line = ctx.line[:i]
			}
		}

		if ctx.matchline("^\\s*\\{") {
			beginBrace = true
		}
		if ctx.matchline("\\{\\s*$") {
			beginBrace = true
		}
		if ctx.matchline("^\\s*}") {
			endBrace = true
		}
		if ctx.matchline("};?\\s*$") {
			endBrace = true
		}
		if ctx.matchline("^\\s*loadLib\\s+\\S") {
			loadLib = true
		}
		if ctx.matchline("^\\s*proc\\s") {
			foundProc = true
		}
		if ctx.matchline("^\\s*if\\s") {
			foundIf = true
		}
		if ctx.matchline("\\[.*\\]") {
			foundBrackets = true
		}
		if ctx.matchline("^\\s*expect\\s") {
			foundExpect = true
		}
	}

	if loadLib && (foundPound || (beginBrace && endBrace)) {
		isExpect = true
	}
	if beginBrace && endBrace &&
		(foundProc || foundIf || foundBrackets || foundExpect) {
		isExpect = true
	}

	if debug > 0 {
		fmt.Fprint(os.Stderr, "expect verifier returned %t on %s\n", isExpect, path)
	}

	return isExpect
}

// reallyPascal - returns  true if filename contents really are Pascal.
func reallyPascal(ctx *countContext, path string) bool {
	//
	// dwheeler had this to say:
	//
	// This isn't as obvious as it seems.
	// Many ".p" files are Perl files
	// (such as /usr/src/redhat/BUILD/ispell-3.1/dicts/czech/glob.p),
	// others are C extractions
	// (such as /usr/src/redhat/BUILD/linux/include/linux/umsdos_fs.p
	// and some files in linuxconf).
	// However, test files in "p2c" really are Pascal, for example.
	//
	// Note that /usr/src/redhat/BUILD/ucd-snmp-4.1.1/ov/bitmaps/UCD.20.p
	// is actually C code.  The heuristics determine that they're not Pascal,
	// but because it ends in ".p" it's not counted as C code either.
	// I believe this is actually correct behavior, because frankly it
	// looks like it's automatically generated (it's a bitmap expressed as code).
	// Rather than guess otherwise, we don't include it in a list of
	// source files.  Let's face it, someone who creates C files ending in ".p"
	// and expects them to be counted by default as C files in SLOCCount needs
	// their head examined.  I suggest examining their head
	// with a sucker rod (see syslogd(8) for more on sucker rods).
	//
	// This heuristic counts as Pascal such files such as:
	//  /usr/src/redhat/BUILD/teTeX-1.0/texk/web2c/tangleboot.p
	// Which is hand-generated.  We don't count woven documents now anyway,
	// so this is justifiable.

	// The heuristic is as follows: it's Pascal _IF_ it has all of the following
	// (ignoring {...} and (*...*) comments):
	// 1. "^..program NAME" or "^..unit NAME",
	// 2. "procedure", "function", "^..interface", or "^..implementation",
	// 3. a "begin", and
	// 4. it ends with "end.",
	//
	// Or it has all of the following:
	// 1. "^..module NAME" and
	// 2. it ends with "end.".
	//
	// Or it has all of the following:
	// 1. "^..program NAME",
	// 2. a "begin", and
	// 3. it ends with "end.".
	//
	// The "end." requirements in particular filter out non-Pascal.
	//
	// Note (jgb): this does not detect Pascal main files in fpc, like
	// fpc-1.0.4/api/test/testterminfo.pas, which does not have "program" in
	// it
	var isPascal bool // Value to determine.

	var hasProgram bool
	var hasUnit bool
	var hasModule bool
	var hasProcedureOrFunction bool
	var hasBegin bool
	var foundTerminatingEnd bool

	ctx.setup(path)
	defer ctx.teardown()

	for ctx.munchline() {
		// Ignore {...} comments on this line; imperfect, but effective.
		ctx.drop("\\{.*?\\}")
		// Ignore (*...*) comments on this line; imperfect but effective.
		ctx.drop("\\(\\*.*\\*\\)")

		if ctx.matchline("(?i)\\bprogram\\s+[A-Za-z]") {
			hasProgram = true
		}
		if ctx.matchline("(?i)\\bunit\\s+[A-Za-z]") {
			hasUnit = true
		}
		if ctx.matchline("(?i)\\bmodule\\s+[A-Za-z]") {
			hasModule = true
		}
		if ctx.matchline("(?i)\\bprocedure\\b") {
			hasProcedureOrFunction = true
		}
		if ctx.matchline("(?i)\\bfunction\\b") {
			hasProcedureOrFunction = true
		}
		if ctx.matchline("(?i)^\\s*interface\\s+") {
			hasProcedureOrFunction = true
		}
		if ctx.matchline("(?i)^\\s*implementation\\s+") {
			hasProcedureOrFunction = true
		}
		if ctx.matchline("(?i)\\bbegin\\b") {
			hasBegin = true
		}
		// Originally dw said: "This heuristic fails if there
		// are multi-line comments after "end."; I haven't
		// seen that in real Pascal programs:"
		// But jgb found there are a good quantity of them in
		// Debian, specially in fpc (at the end of a lot of
		// files there is a multiline comment with the
		// changelog for the file).  Therefore, assume Pascal
		// if "end." appears anywhere in the file.
		if ctx.matchline("(?i)end\\.\\s*$") {
			foundTerminatingEnd = true
		}
	}

	// Okay, we've examined the entire file looking for clues;
	// let's use those clues to determine if it's really Pascal:
	isPascal = (((hasUnit || hasProgram) && hasProcedureOrFunction &&
		hasBegin && foundTerminatingEnd) ||
		(hasModule && foundTerminatingEnd) ||
		(hasProgram && hasBegin && foundTerminatingEnd))

	if debug > 0 {
		fmt.Fprint(os.Stderr, "pascal verifier returned %t on %s\n", isPascal, path)
	}

	return isPascal
}

func wasGeneratedAutomatically(ctx *countContext, path string, eolcomment string) bool {
	// Determine if the file was generated automatically.
	// Use a simple heuristic: check if first few lines have phrases like
	// "generated automatically", "automatically generated", "Generated by",
	// or "do not edit" as the first
	// words in the line (after possible comment markers and spaces).
	i := 15 // Look at first 15 lines.
	ctx.setup(path)
	defer ctx.teardown()

	// Avoid blowing up if the comment leader is "*" (as in COBOL).
	if eolcomment == "*" {
		eolcomment = ""
	} else {
		eolcomment = "|" + eolcomment
	}
	re := "(\\*" + eolcomment + ").*(?i:" + generated + ")"
	cre, err := regexp.Compile(re)
	if err != nil {
		panic(fmt.Sprintf("unexpected failure while building %s", re))
	}

	for ctx.munchline() && i > 0 {
		//fmt.Fprint(os.Stderr, "Matching %s against %s", ctx.line, re)
		if cre.Find(ctx.line) != nil {
			if debug > 0 {
				fmt.Fprint(os.Stderr, "%s: is generated\n", path)
			}
			return true
		}
		i--
	}

	return false
}

// hashbang - hunt for a specified string in the first line of an executable
func hashbang(ctx *countContext, path string, langname string) bool {
	fi, err := os.Stat(path)
	// If it's not executable by somebody, don't read for hashbang
	if err != nil || (fi.Mode()&01111) == 0 {
		return false
	}
	ctx.setup(path)
	defer ctx.teardown()
	s, err := ctx.rc.ReadString('\n')
	return err == nil && strings.HasPrefix(s, "#!") && strings.Contains(s, langname)
}

// cFamilyCounter - Count the SLOC in a C-family source file
//
// C++ headers get counted as C. This can only be fixed in postprocessing
// by noticing that there are no files with a C extension in the tree.
//
// Another minor issue is that it's possible for the antecedents in Lex rules
// to look like C comment starts. In theory we could fix this by requiring Lex
// files to contain %%.
func cFamilyCounter(ctx *countContext, path string, syntax genericLanguage) (uint, uint) {
	/* Types of comments: */
	const commentBLOCK = 0
	const commentTRAILING = 1

	mode := stateNORMAL /* stateNORMAL, stateINSTRING, stateINMULTISTRING, or stateINCOMMENT */
	var sloc uint
	var lloc uint
	var commentType int /* commentBLOCK or commentTRAILING */
	var startline uint

	if syntax.verifier != nil && !syntax.verifier(ctx, path) {
		return 0, 0
	}

	ctx.setup(path)
	defer ctx.teardown()

	// # at start of file - assume it's a cpp directive
	if ctx.consume([]byte("#")) {
		lloc++
	}
	for {
		c, err := ctx.getachar()
		if err == io.EOF {
			break
		}

		if mode == stateNORMAL {
			if !ctx.lexfile && c == '"' {
				ctx.nonblank = true
				mode = stateINSTRING
				startline = ctx.lineNumber
			} else if (syntax.flags & cbs) != 0 && !ctx.lexfile && c == '\'' {
				/* Consume single-character 'xxxx' values */
				ctx.nonblank = true
				c, err = ctx.getachar()
				if c == '\\' {
					c, err = ctx.getachar()
				}
				for {
					c, err = ctx.getachar()
					if (c == '\'') || (c == '\n') || (err == io.EOF) {
						break
					}
				}
			} else if (c == syntax.commentleader[0]) && (ctx.ispeek(syntax.commentleader[1])) {
				c, err = ctx.getachar()
				mode = stateINCOMMENT
				commentType = commentBLOCK
				startline = ctx.lineNumber
			} else if (syntax.eolcomment != "") && c == syntax.eolcomment[0] && (len(syntax.eolcomment) > 1 && ctx.ispeek(syntax.eolcomment[1])) {
				c, _ = ctx.getachar()
				mode = stateINCOMMENT
				commentType = commentTRAILING
				startline = ctx.lineNumber
			} else if (syntax.multistring != "") && (c == syntax.multistring[0]) {
				mode = stateINMULTISTRING
				startline = ctx.lineNumber
			} else if (syntax.flags & gotick) != 0 && c == '`' {
				startLine := ctx.lineNumber
				for {
					c, err = ctx.getachar()
					if err != nil {
						fmt.Fprint(os.Stderr, "WARNING - unterminated backtick, line %d, file %s\n", startLine, path)
					}
					if c == '`' {
						break
					}
				}
			} else if !isspace(c) {
				ctx.nonblank = true
			}
		} else if mode == stateINSTRING {
			// We only count string lines with non-whitespace --
			// this is to gracefully handle syntactically invalid
			// programs.  You could argue that multiline strings
			// with whitespace are still executable and should be
			// counted.
			if !isspace(c) {
				ctx.nonblank = true
			}
			if c == '"' {
				mode = stateNORMAL
			} else if (syntax.flags & cbs) != 0 && (c == '\\') && (ctx.ispeek('"') || ctx.ispeek('\\')) {
				c, _ = ctx.getachar()
			} else if (syntax.flags & cbs) != 0 && (c == '\\') && ctx.ispeek('\n') {
				c, _ = ctx.getachar()
			} else if c == '\n' {
				// We found a bare newline in a string without
				// preceding backslash.
				if (syntax.flags & eolwarn) != 0 {
					fmt.Fprint(os.Stderr, "WARNING - newline in string, line %d, file %s\n", ctx.lineNumber, path)
				}

				// We COULD warn & reset mode to
				// "Normal", but lots of code does this,
				// so we'll just depend on the warning
				// for ending the program in a string to
				// catch syntactically erroneous
				// programs.
			}
		} else if mode == stateINMULTISTRING {
			// We only count multi-string lines with non-whitespace.
			if !isspace(c) {
				ctx.nonblank = true
			}
			if c == syntax.multistring[0] {
				mode = stateNORMAL
			}
		} else { /* stateINCOMMENT mode */
			if (c == '\n') && (commentType == commentTRAILING) {
				mode = stateNORMAL
			}
			if (commentType == commentBLOCK) && (c == syntax.commenttrailer[0]) && ctx.ispeek(syntax.commenttrailer[1]) {
				c, _ = ctx.getachar()
				mode = stateNORMAL
			}
		}
		if c == '\n' {
			if ctx.nonblank {
				sloc++
			}
			ctx.nonblank = false
			if ctx.consume([]byte("%")) {
				ctx.lexfile = true
				ctx.nonblank = true
			}
			// # at start of line - assume it's a cpp directive
			if ctx.consume([]byte("#")) {
				lloc++
			}
		}
		if mode == stateNORMAL && len(syntax.terminator) > 0 && c == syntax.terminator[0] {
			lloc++
		}
	}
	/* We're done with the file.  Handle EOF-without-EOL. */
	if ctx.nonblank {
		sloc++
	}
	ctx.nonblank = false
	if (mode == stateINCOMMENT) && (commentType == commentTRAILING) {
		mode = stateNORMAL
	}

	if mode == stateINCOMMENT {
		fmt.Fprint(os.Stderr, "%q, line %d: ERROR - terminated in comment beginning here\n",
			path, startline)
	} else if mode == stateINSTRING {
		fmt.Fprint(os.Stderr, "%q, line %d: ERROR - terminated in string beginning here\n",
			path, startline)
	}

	return sloc, lloc
}

// genericCounter - count SLOC in a generic language.
func genericCounter(ctx *countContext,
	path string, eolcomment string, terminator string,
	verifier func(*countContext, string) bool) (uint, uint) {
	var sloc uint
	var lloc uint

	if verifier != nil && !verifier(ctx, path) {
		return 0, 0
	}

	ctx.setup(path)
	defer ctx.teardown()

	for ctx.munchline() {
		i := bytes.Index(ctx.line, []byte(eolcomment))
		if i > -1 {
			ctx.line = ctx.line[:i]
		}
		ctx.line = bytes.Trim(ctx.line, " \t\r\n")
		if len(ctx.line) > 0 {
			sloc++
			if len(terminator) > 0 && strings.Contains(string(ctx.line), terminator) {
				lloc++
			}
		}
	}

	return sloc, lloc
}

func pythonCounter(ctx *countContext, path string) (uint, uint) {
	var sloc uint
	var lloc uint
	var isintriple bool  // A triple-quote is in effect.
	var isincomment bool // We are in a multiline (triple-quoted) comment.

	ctx.setup(path)
	defer ctx.teardown()

	tripleBoundary := func(line []byte) bool { return bytes.Contains(line, []byte(dt)) || bytes.Contains(line, []byte(st)) }
	for ctx.munchline() {
		// Delete trailing comments
		i := bytes.Index(ctx.line, []byte("#"))
		if i > -1 {
			ctx.line = ctx.line[:i]
		}

		if !isintriple { // Normal case:
			// Ignore triple-quotes that begin & end on the ctx.line.
			ctx.line = dtriple.ReplaceAllLiteral(ctx.line, []byte(""))
			ctx.line = striple.ReplaceAllLiteral(ctx.line, []byte(""))
			// Delete lonely strings starting on BOL.
			ctx.line = dlonely.ReplaceAllLiteral(ctx.line, []byte(""))
			ctx.line = slonely.ReplaceAllLiteral(ctx.line, []byte(""))
			// Delete trailing comments
			i := bytes.Index(ctx.line, []byte("#"))
			if i > -1 {
				ctx.line = ctx.line[:i]
			}
			// Does multi-line triple-quote begin here?
			if tripleBoundary(ctx.line) {
				isintriple = true
				ctx.line = bytes.Trim(ctx.line, " \t\r\n")
				// It's a comment if at BOL.
				if bytes.HasPrefix(ctx.line, []byte(dt)) || bytes.HasPrefix(ctx.line, []byte(st)) {
					isincomment = true
				}
			}
		} else { // we ARE in a triple.
			if tripleBoundary(ctx.line) {
				if isincomment {
					// Delete text if it's a comment (not if data)
					ctx.line = dtrailer.ReplaceAllLiteral(ctx.line, []byte(""))
					ctx.line = strailer.ReplaceAllLiteral(ctx.line, []byte(""))
				} else {
					// Leave something there to count.
					ctx.line = dtrailer.ReplaceAllLiteral(ctx.line, []byte("x"))
					ctx.line = strailer.ReplaceAllLiteral(ctx.line, []byte("x"))
				}
				// But wait!  Another triple might
				// start on this ctx.line!  (see
				// Python-1.5.2/Tools/freeze/makefreeze.py
				// for an example)
				if tripleBoundary(ctx.line) {
					// It did!  No change in state!
				} else {
					isintriple = false
					isincomment = false
				}
			}
		}
		ctx.line = bytes.Trim(ctx.line, " \t\r\n")
		if !isincomment && len(ctx.line) > 0 {
			sloc++
			if ctx.line[len(ctx.line)-1] != '\\' {
				lloc++
			}
		}
	}

	return sloc, lloc
}

// perlCounter - count SLOC in Perl
//
// Physical lines of Perl are MUCH HARDER to count than you'd think.
// Comments begin with "#".
// Also, anything in a "perlpod" is a comment.
// See perlpod(1) for more info; a perlpod starts with
// \s*=command, can have more commands, and ends with \s*=cut.
// Note that = followed by space is NOT a perlpod.
// Although we ignore everything after __END__ in a file,
// we will count everything after __DATA__; there's arguments for counting
// and for not counting __DATA__.
//
// What's worse, "here" documents must be COUNTED AS CODE, even if
// they're FORMATTED AS A PERLPOD.  Surely no one would do this, right?
// Sigh... it can happen. See perl5.005_03/pod/splitpod.
func perlCounter(ctx *countContext, path string) (uint, uint) {
	var sloc uint
	var lloc uint
	var heredoc string
	var isinpod bool

	ctx.setup(path)
	defer ctx.teardown()

	for ctx.munchline() {
		// Delete trailing comments
		i := bytes.Index(ctx.line, []byte("#"))
		if i > -1 {
			ctx.line = ctx.line[:i]
		}

		ctx.line = bytes.Trim(ctx.line, " \t\r\n")

		if heredoc != "" && strings.HasPrefix(string(ctx.line), heredoc) {
			heredoc = "" //finished here doc.
		} else if i := bytes.Index(ctx.line, []byte("<<")); i > -1 {
			// Beginning of a here document.
			heredoc = string(bytes.Trim(ctx.line[i:], "< \t\"';,"))
		} else if len(heredoc) == 0 && bytes.HasPrefix(ctx.line, []byte("=cut")) {
			// Ending a POD?
			if !isinpod {
				fmt.Fprint(os.Stderr, "%q, %d: cut without pod start\n",
					path, ctx.lineNumber)
			}
			isinpod = false
			continue // Don't count the cut command.
		} else if len(heredoc) == 0 && podheader.Match(ctx.line) {
			// Starting or continuing a POD?
			// Perlpods can have multiple contents, so
			// it's okay if isinpod == true.  Note that
			// =(space) isn't a POD; library file
			// perl5db.pl does this!
			isinpod = true
		} else if bytes.HasPrefix(ctx.line, []byte("__END__")) {
			// Stop processing this file on __END__.
			break
		}
		if !isinpod && len(ctx.line) > 0 {
			sloc++
			if strings.Contains(string(ctx.line), ";") {
				lloc++
			}
		}
	}

	return sloc, lloc
}

// pascalCounter - Handle lanuages like Pascal and Modula 3
func pascalCounter(ctx *countContext, path string, syntax pascalLike) (uint, uint) {
	mode := stateNORMAL /* stateNORMAL, or stateINCOMMENT */
	var sloc uint
	var lloc uint
	var startline uint

	if syntax.verifier != nil && !syntax.verifier(ctx, path) {
		return 0, 0
	}

	ctx.setup(path)
	defer ctx.teardown()

	for {
		c, err := ctx.getachar()
		if err == io.EOF {
			break
		}

		if mode == stateNORMAL {
			if syntax.bracketcomments && c == '{' {
				mode = stateINCOMMENT
			} else if (c == '(') && ctx.ispeek('*') {
				c, _ = ctx.getachar()
				mode = stateINCOMMENT
			} else if !isspace(c) {
				ctx.nonblank = true
			} else if c == '\n' {
				if ctx.nonblank {
					sloc++
				}
				ctx.nonblank = false
			}
			if len(syntax.terminator) > 0 &&  c == syntax.terminator[0] {
				lloc++
			}
		} else { /* stateINCOMMENT mode */
			if syntax.bracketcomments && c == '}' {
				mode = stateNORMAL
			} else if (c == '*') && ctx.ispeek(')') {
				_, _ = ctx.getachar()
				mode = stateNORMAL
			}
		}
	}
	/* We're done with the file.  Handle EOF-without-EOL. */
	if ctx.nonblank {
		sloc++
	}
	ctx.nonblank = false

	if mode == stateINCOMMENT {
		fmt.Fprint(os.Stderr, "%q, line %d: ERROR - terminated in comment beginning here.\n",
			path, startline)
	} else if mode == stateINSTRING {
		fmt.Fprint(os.Stderr, "%q, line %d: ERROR - terminated in string beginning here.\n",
			path, startline)
	}

	return sloc, lloc
}

func fortranCounter(ctx *countContext, path string, syntax fortranLike) uint {
	var sloc uint

	ctx.setup(path)
	defer ctx.teardown()

	for ctx.munchline() {
		if !(syntax.comment.Match(ctx.line) && !syntax.nocomment.Match(ctx.line)) {
			sloc++
		}
	}
	return sloc
}

// Generic - recognize lots of languages with generic syntax
func Generic(ctx *countContext, path string) SourceStat {
	var stat SourceStat

	autofilter := func(eolcomment string) bool {
		if wasGeneratedAutomatically(ctx, path, eolcomment) {
			if debug > 0 {
				fmt.Printf("automatic generation filter failed: %s\n", path)
			}
			return true
		}
		if debug > 0 {
			fmt.Printf("automatic generation filter passed: %s\n", path)
		}
		return false
	}

	for i := range genericLanguages {
		lang := genericLanguages[i]
		if strings.HasSuffix(path, lang.suffix) {
			if autofilter(lang.eolcomment) {
				return stat
			} else if len(lang.commentleader) > 0 {
				stat.SLOC, stat.LLOC = cFamilyCounter(ctx, path, lang)
			} else {
				stat.SLOC, stat.LLOC = genericCounter(ctx, path,
					lang.eolcomment, lang.terminator,
					lang.verifier)
			}
			if stat.SLOC > 0 {
				stat.Language = lang.name
				return stat
			}
		}
	}

	if strings.HasSuffix(path, ".py") || hashbang(ctx, path, "python") {
		if autofilter("#") {
			return stat
		}
		stat.Language = "python"
		stat.SLOC, stat.LLOC = pythonCounter(ctx, path)
		return stat
	}

	if strings.HasSuffix(path, ".pl") || strings.HasSuffix(path, ".pm") || strings.HasSuffix(path, ".ph") || hashbang(ctx, path, "perl") {
		if autofilter("#") {
			return stat
		}
		stat.Language = "perl"
		stat.SLOC, stat.LLOC = perlCounter(ctx, path)
		return stat
	}

	if filepath.Base(path) == "wscript" {
		if autofilter("#") {
			return stat
		}
		stat.Language = "waf"
		stat.SLOC, stat.LLOC = pythonCounter(ctx, path)
		return stat
	}

	for i := range scriptingLanguages {
		if autofilter("#") {
			return stat
		}
		lang := scriptingLanguages[i]
		if strings.HasSuffix(path, lang.suffix) || hashbang(ctx, path, lang.hashbang) {
			stat.Language = lang.name
			stat.SLOC, stat.LLOC = genericCounter(ctx, path, "#", "", nil)
			return stat
		}
	}

	for i := range pascalLikes {
		lang := pascalLikes[i]
		if strings.HasSuffix(path, lang.suffix) {
			stat.Language = lang.name
			stat.SLOC, stat.LLOC = pascalCounter(ctx, path, lang)
			if stat.SLOC > 0 {
				return stat
			}
		}
	}

	for i := range fortranLikes {
		lang := fortranLikes[i]
		if strings.HasSuffix(path, lang.suffix) {
			stat.Language = lang.name
			stat.SLOC = fortranCounter(ctx, path, lang)
			if stat.SLOC > 0 {
				return stat
			}
		}
	}

	return stat
}

func isDirectory(path string) bool {
	fileInfo, err := os.Stat(path)
	return err == nil && fileInfo.Mode().IsDir()
}

func isRegular(path string) bool {
	fileInfo, err := os.Stat(path)
	return err == nil && fileInfo.Mode().IsRegular()
}

// filter - winnows out uninteresting paths before handing them to process
func filter(path string, info os.FileInfo, err error) error {
	if debug > 0 {
		fmt.Printf("entering filter: %s\n", path)
	}
	suffix := filepath.Ext(path)
	if suffix != "" && neverInterestingBySuffix[suffix] {
		if debug > 0 {
			fmt.Printf("suffix filter failed: %s\n", path)
		}
		return err
	}
	for i := range neverInterestingByPrefix {
		if strings.HasPrefix(path, neverInterestingByPrefix[i]) {
			if debug > 0 {
				fmt.Printf("prefix filter failed: %s\n", path)
			}
			return err
		}
	}
	for i := range neverInterestingByInfix {
		if strings.Contains(path, neverInterestingByInfix[i]) {
			if debug > 0 {
				fmt.Printf("infix filter failed: %s\n", path)
			}
			if isDirectory(path) {
				if debug > 0 {
					fmt.Printf("directory skipped: %s\n", path)
				}
				return filepath.SkipDir
			}
			return err
		}
	}
	basename := filepath.Base(path)
	if neverInterestingByBasename[strings.ToLower(basename)] {
		if debug > 0 {
			fmt.Printf("basename filter failed: %s\n", path)
		}
		return err
	}
	if exclusions != nil && exclusions.MatchString(path) {
		if debug > 0 {
			fmt.Printf("exclusion '%s' filter failed: %s\n", exclusions, path)
		}
		return err
	}

	/* has to come after the infix check for directory */
	if !isRegular(path) {
		if debug > 0 {
			fmt.Printf("regular-file filter failed: %s\n", path)
		}
		return err
	}

	/* toss generated Makefiles */
	if basename == "Makefile" {
		if _, err := os.Stat(path + ".in"); err == nil {
			if debug > 0 {
				fmt.Printf("generated-makefile filter failed: %s\n", path)
			}
			return err
		}
	}

	if debug > 0 {
		fmt.Printf("passed filter: %s\n", path)
	}

	// Now the real work gets done
	ctx := new(countContext)
	st := Generic(ctx, path)
	st.Path = path
	pipeline <- st

	return err
}

type countRecord struct {
	language  string
	slinecount uint
	llinecount uint
	filecount uint
}


func cocomo81(sloc uint) float64 {
	const cTIMEMULT = 2.4
	const cTIMEEXP = 1.05
	fmt.Printf("\nTotal Physical Source Lines of Code (SLOC)                = %d\n", sloc)
	fmt.Printf(" (COCOMO I model, Person-Months = %2.2f * (KSLOC**%2.2f))\n", cTIMEMULT, cTIMEEXP)
	return cTIMEMULT * math.Pow(float64(sloc)/1000, cTIMEEXP)
}

// See https://en.wikipedia.org/wiki/COCOMO
func cocomo2000(lloc uint) float64 {
	const cTIMEMULT = 3.2
	const cTIMEEXP = 1.05
	fmt.Printf("\nTotal Logical Source Lines of Code (LLOC)                 = %d\n", lloc)
	fmt.Printf(" (COCOMO II model, Person-Months = %2.2f * (KLOC**%2.2f))\n", cTIMEMULT, cTIMEEXP)
	return cTIMEMULT * math.Pow(float64(lloc)/1000, cTIMEEXP)
}

func reportCocomo(loc uint, curve func(uint) float64) {
	const cSCHEDMULT = 2.5
	const cSCHEDEXP = 0.38
	const cSALARY = 790000 // From Wikipedia, late 2019
	const cOVERHEAD = 2.40
	personMonths := curve(loc)
	fmt.Printf("Development Effort Estimate, Person-Years (Person-Months) = %2.2f (%2.2f)\n", personMonths/12, personMonths)
	schedMonths := cSCHEDMULT * math.Pow(personMonths, cSCHEDEXP)
	fmt.Printf("Schedule Estimate, Years (Months)                         = %2.2f (%2.2f)\n", schedMonths/12, schedMonths)
	fmt.Printf(" (COCOMO model, Months = %2.2f * (person-months**%2.2f))\n", cSCHEDMULT, cSCHEDEXP)
	fmt.Printf("Estimated Average Number of Developers (Effort/Schedule)  = %2.2f\n", personMonths/schedMonths)
	fmt.Printf("Total Estimated Cost to Develop                           = $%d\n", int(cSALARY*(personMonths/12)*cOVERHEAD))
	fmt.Printf(" (average salary = $%d/year, overhead = %2.2f).\n", cSALARY, cOVERHEAD)
}

func listLanguages(lloc bool) []string {
	names := []string{"python", "waf", "perl"}
	var lastlang string
	for i := range genericLanguages {
		lang := genericLanguages[i].name
		if lang != lastlang {
			if !lloc || len(genericLanguages[i].terminator) > 0 {
				names = append(names, lang)
				lastlang = lang
			}
		}
	}

	for i := range pascalLikes {
		lang := pascalLikes[i].name
		if lang != lastlang {
			if !lloc || len(pascalLikes[i].terminator) > 0 {
				names = append(names, lang)
				lastlang = lang
			}
		}
	}

	if !lloc {
		for i := range scriptingLanguages {
			lang := scriptingLanguages[i].name
			if lang != lastlang {
				names = append(names, lang)
				lastlang = lang
			}
		}

		for i := range fortranLikes {
			lang := fortranLikes[i].name
			if lang != lastlang {
				names = append(names, lang)
				lastlang = lang
			}
		}
	}
	sort.Strings(names)
	return names
}

func listExtensions() {
	extensions := map[string][]string{
		"python": {".py"},
		"waf":    {"waf"},
		"perl":   {"pl", "pm"},
	}
	for i := range genericLanguages {
		lang := genericLanguages[i]
		extensions[lang.name] = append(extensions[lang.name], lang.suffix)
	}

	for i := range scriptingLanguages {
		lang := scriptingLanguages[i]
		extensions[lang.name] = append(extensions[lang.name], lang.suffix)
	}

	for i := range pascalLikes {
		lang := pascalLikes[i]
		extensions[lang.name] = append(extensions[lang.name], lang.suffix)
	}

	for i := range fortranLikes {
		lang := fortranLikes[i]
		extensions[lang.name] = append(extensions[lang.name], lang.suffix)
	}
	names := listLanguages(false)
	for i := range names {
		fmt.Printf("%s: %v\n", names[i], extensions[names[i]])
	}
}

type sortable []countRecord

func (a sortable) Len() int           { return len(a) }
func (a sortable) Swap(i int, j int)  { a[i], a[j] = a[j], a[i] }
func (a sortable) Less(i, j int) bool { return -a[i].slinecount < -a[j].slinecount }

var cpuprofile = flag.String("cpuprofile", "", "write cpu profile to file")

func main() {
	var individual bool
	var unclassified bool
	var llist bool
	var slist bool
	var extensions bool
	var cocomo bool
	var json bool
	var showversion bool
	excludePtr := flag.String("x", "",
		"paths and directories to exclude")
	flag.BoolVar(&individual, "i", false,
		"list counts and types for individual files")
	flag.BoolVar(&unclassified, "u", false,
		"list unclassified files")
	flag.BoolVar(&cocomo, "c", false,
		"report Cocomo-model estimation")
	flag.BoolVar(&llist, "l", false,
		"list languages that yield LLOC and exit")
	flag.BoolVar(&slist, "s", false,
		"list languages that yield SLOC and exit")
	flag.BoolVar(&extensions, "e", false,
		"list extensions associated with each language and exit")
	flag.IntVar(&debug, "d", 0,
		"set debug level")
	flag.BoolVar(&json, "j", false,
		"dump statistics in JSON format")
	flag.BoolVar(&showversion, "V", false,
		"report version and exit")
	flag.Parse()

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}
	if showversion {
		fmt.Printf("loccount %s\n", version)
		return
	} else if slist {
		ll := listLanguages(false)
		if !individual {
			fmt.Printf("%d: %s\n", len(ll), ll)
		} else {
			for _, lang := range ll {
				fmt.Printf("%s\n", lang)
			}
		}
		return
	} else if llist {
		ll := listLanguages(true)
		if !individual {
			fmt.Printf("%d: %s\n", len(ll), ll)
		} else {
			for _, lang := range ll {
				fmt.Printf("%s\n", lang)
			}
		}
		return
	} else if extensions {
		listExtensions()
		return
	}

	individual = individual || unclassified

	// For maximum performance, make the pipeline be as deep as the
	// number of processor we have available, that way the machine will
	// be running full-out exactly when it's filled and no sooner.
	// This makes order of output nondeterministic, which is why
	// we sometimes want to disable it.
	var chandepth int
	if individual || unclassified {
		chandepth = 0
	} else {
		chandepth = runtime.NumCPU()
	}
	pipeline = make(chan SourceStat, chandepth)

	if len(*excludePtr) > 0 {
		exclusions = regexp.MustCompile(*excludePtr)
	}
	roots := flag.Args()

	here, _ := os.Getwd()
	go func() {
		for i := range roots {
			fi, err := os.Stat(roots[i])
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				break
			}
			if fi.Mode().IsDir() {
				os.Chdir(roots[i])
				// The system filepath.Walk() works here,
				// but is slower.
				walk(".", filter)
				os.Chdir(here)
			} else {
				filter(roots[i], fi, nil)
			}
		}
		close(pipeline)
	}()

	var totals countRecord
	counts := map[string]countRecord{}

	// Mainline resumes
	for {
		st, more := <-pipeline
		if !more {
			break
		}
		if debug > 0 {
			fmt.Printf("from pipeline: %s %d %s\n",
				st.Path, st.SLOC, st.Language)
		}

		if individual {
			if !unclassified && st.SLOC > 0 {
				fmt.Printf("%s %s %d %d\n",
					st.Path, st.Language, st.SLOC, st.LLOC)
			} else if unclassified && st.SLOC == 0 {
				// Not a recognized source type,
				// nor anything we know to discard
				fmt.Println(st.Path)
			}
			continue
		}

		if st.SLOC > 0 {
			var tmp = counts[st.Language]
			tmp.language = st.Language
			tmp.slinecount += st.SLOC
			tmp.llinecount += st.LLOC
			tmp.filecount++
			counts[st.Language] = tmp
			totals.slinecount += st.SLOC
			totals.llinecount += st.LLOC
			totals.filecount++
		}
	}

	if individual {
		return
	}

	// C headers may get reassigned based on what other languages
	// are present in the tree
	if counts["c-header"].slinecount > 0 {
		for i := range cHeaderPriority {
			if counts[cHeaderPriority[i]].slinecount > 0 {
				var tmp = counts[cHeaderPriority[i]]
				tmp.slinecount += counts["c-header"].slinecount
				counts[cHeaderPriority[i]] = tmp
				delete(counts, "c-header")
				break
			}
		}
	}

	var summary sortable
	totals.language = "all"
	if totals.filecount > 1 {
		summary = append(summary, totals)
	}
	for _, v := range counts {
		summary = append(summary, v)
	}

	sort.Sort(summary)
	for i := range summary {
		r := summary[i]
		if json {
			fmt.Printf("{\"language\":%q, \"sloc\":%d, \"lloc\":%d, \"filecount\":%d}\n",
				r.language,
				r.slinecount,
				r.llinecount,
				r.filecount)
		} else {
			fmt.Printf("%-12s SLOC=%-7d (%2.2f%%)\tLLOC=%-7d in %d files\n",
				r.language,
				r.slinecount,
				float64(r.slinecount)*100.0/float64(totals.slinecount),
				r.llinecount,
				r.filecount)
		}
	}

	if cocomo {
		reportCocomo(totals.slinecount, cocomo81)
		reportCocomo(totals.llinecount, cocomo2000)
	}
}

// end
