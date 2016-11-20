package main

import "bytes"
import "bufio"
import "fmt"
import "flag"
import "io"
import "math"
import "os"
import "path/filepath"
import "regexp"
import "runtime"
import "sort"
import "strings"
import "log"

/*
How to add support for a language to this program:

All the language-specific information this program needs to know to do
its job is the syntax of comments and string literals.  Generally,
languages fall into one of the following groups:

* C-like: C is the prototype. This code recognizes them by file
  extension only.  These languages have two kinds of comment.  One is
  a block comment delimited by two distinct strings and the second is
  a winged comment introduced by a third string and terminated by
  newline.  You can add support simply by appending an initializer to
  the cLikes table.

* Scripting languages have only winged comments, usually led with #.  This
  code recognizes them by file extension, or by looking for a hashbang line
  identifying the interpreter.  You can append an initializer to the
  scriptingLanguages table specifying a name, an extension, a matching
  string to look for in a hashbang line, the set of string quotes the
  language uses, and the comment leader. You may additionally specify
  a list of enclosure pairs for multiline literals. That list may 
  begin with "<<", which means the language supports here-doc syntax.

* Generic languages have only winged comments, usually led with #.
  This code recognizes them by file extension only.  You can append an
  initializer to the genericLanguages table specifying a name, an
  extension, string to look for in a hashbang line, and the comment
  leader. For this group the set of string quotes the language uses
  is assumed to include both single and double quote.

* Pascal-likes use the (* *) block comment syntax.  This code
  recognizes them by file extension only.  You can append an
  initializer to the PascalLikes table specifying a name, an
  extension, and a boolean saying whether the language uses { } as
  additional pair of block comments.

* Fortran-likes use various start-of-line characters as comment leaders.
  This code recognizes them by file extension only.  You can append an
  initializer to the fortranLikes table specifying a pair of regular
  expressions; comments are recognized by matching the first and not
  the second.

You may add multiple entries with the same language name, but extensions
must be unique across all tables.
*/

type SourceStat struct {
	Path string
	Language string
	SLOC uint
}

var exclusions []string
var pipeline chan SourceStat

// Data tables driving the recognition and counting of classes of languages.

type cLike struct {
	name string
	extension string
	commentleader string
	commenttrailer string
	eolcomment string
}
var cLikes []cLike

type scriptingLanguage struct {
	name string
	suffix string
	hashbang string
}
var scriptingLanguages []scriptingLanguage

type genericLanguage struct {
	name string
	suffix string
	eolcomment string
}
var genericLanguages []genericLanguage

type pascalLike struct {
	name string
	suffix string
	bracketcomments bool
}
var pascalLikes []pascalLike

var podheader *regexp.Regexp

type fortranLike struct {
	name string
	suffix string
	comment *regexp.Regexp
	nocomment *regexp.Regexp
}
var fortranLikes []fortranLike

var neverInterestingByPrefix []string
var neverInterestingByInfix []string
var neverInterestingBySuffix []string
var neverInterestingByBasename []string

var cHeaderPriority []string

func init() {
	cLikes = []cLike{
		{"c", ".c", "/*", "*/", "//"},
		{"c-header", ".h", "/*", "*/", "//"},
		{"yacc", ".y", "/*", "*/", "//"},
		{"lex", ".l", "/*", "*/", "//"},
		{"c++", ".cpp", "/*", "*/", "//"},
		{"c++", ".cxx", "/*", "*/", "//"},
		{"java", ".java", "/*", "*/", "//"},
		{"obj-c", ".m", "/*", "*/", "//"},
		{"c#", ".cs", "/*", "*/", "//"},
		{"php", ".php", "/*", "*/", "//"},
		{"go", ".go", "/*", "*/", "//"},
		{"swift", ".swift", "/*", "*/", "//"},
		{"autotools", "config.h.in", "/*", "*/", "//"},
		{"sql", ".sql", "/*", "*/", "--"},
		{"haskell", ".hs", "{-", "-}", "--"},
		{"pl/1", ".pl1", "/*", "*/", ""},
		{"asm", ".asm", "/*", "*/", ";"},
		{"asm", ".s", "/*", "*/", ";"},
		{"asm", ".S", "/*", "*/", ";"},
	}
	scriptingLanguages = []scriptingLanguage{
		{"tcl", ".tcl", "tcl"},	/* must be before sh */
		{"csh", ".csh", "csh"},
		{"shell", ".sh", "sh"},
		{"ruby", ".rb", "ruby"},
		{"awk", ".awk", "awk"},
		{"sed", ".sed", "sed"},
	}
	genericLanguages = []genericLanguage{
		{"ada", ".ada", "--"},
		{"ada", ".adb", "--"},
		{"ada", ".ads", "--"},
		{"ada", ".pad", "--"},	// Oracle Ada preprocessor.
		{"makefile", ".mk", "#"},
		{"makefile", "Makefile", "#"},
		{"makefile", "makefile", "#"},
		{"makefile", "Imakefile", "#"},
		{"m4", ".m4", "#"},
		{"lisp", ".lisp", ";"},
		{"lisp", ".lsp", ";"},	// XLISP
		{"lisp", ".cl", ";"},	// Common Lisp
		{"scheme", ".scm", ";"},
		{"elisp", ".el", ";"},	// Emacs Lisp
		{"cobol", ".CBL", "*"},
		{"cobol", ".cbl", "*"},
		{"cobol", ".COB", "*"},
		{"cobol", ".cob", "*"},
		{"eiffel", ".e", "--"},
		{"sather", ".sa", "--"},
		{"lua", ".lua", "--"},
		{"clu", ".clu", "%"},
		{"rust", ".rs", "//"},
		{"rust", ".rlib", "//"},
		{"erlang", ".erl", "%"},
		{"turing", ".t", "%"},
		{"d", ".d", "//"},
		// autoconf cruft - note the config.h-in entry under C-likes
		{"autotools", "autogen.sh", "#"},
		{"autotools", "configure.in", "#"},
		{"autotools", "Makefile.in", "#"},
		{"autotools", ".am", "#"},
		{"autotools", ".ac", "#"},
		{"autotools", ".mf", "#"},
		// Scons
		{"scons", "SConstruct", "#"},
	}
	pascalLikes = []pascalLike{
		{"pascal", ".pas", true},
		{"modula3", ".i3", false},
		{"modula3", ".m3", false},
		{"modula3", ".ig", false},
		{"modula3", ".mg", false},
		{"ml",      ".ml", false},
		{"oberon",  ".mod", false},
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
		{"fortran", ".f77", f77comment, f77nocomment},
		{"fortran", ".f", f77comment, f77nocomment},
	}

	var perr error
	podheader, perr = regexp.Compile("=[a-zA-Z]")
	if perr != nil {
		panic(perr)
	}
	
	neverInterestingByPrefix = []string{"."}
	neverInterestingByInfix = []string{".so.", "/."}
	neverInterestingBySuffix = []string{"~",
		".a", ".la", ".o", ".so",
		".gif", ".jpg", ".jpeg", ".ico", ".xpm", ".xbm", ".bmp",
		".ps", ".pdf", ".eps",
		".tfm", ".ttf", ".bdf", ".afm",
		".fig", ".pic",
		".pyc", ".pyo", ".elc",
		".1", ".2", ".3", ".4", ".5", ".6", ".7", ".8", ".n", ".man",
		".html", ".htm", ".sgml", ".xml", ".txt", ".tex",
		".po",
		".gz", ".bz2", ".Z", ".tgz", ".zip",
		".au", ".wav", ".ogg",
	}
	neverInterestingByBasename = []string{
		"README", "Readme", "readme", "README.tk", "Changelog",
		"ChangeLog", "Repository", "CHANGES", "Changes",
		"BUGS", "TODO", "COPYING", "MAINTAINERS", "NEWS",
		"configure", "autom4te.cache", "config.log", "config.status",
	}
	cHeaderPriority = []string{"c", "c++", "obj-c"}
}

// Generic machinery for walking source text to count lines

/* Modes */
const NORMAL = 0
const INSTRING = 1
const INCOMMENT = 2

type countContext struct {
	line_number uint
	nonblank bool			// Is current line nonblank?
	last_char_was_newline bool
	underlyingStream *os.File
	rc *bufio.Reader
	lastpath string
}

func (ctx *countContext) Setup(path string) bool {
	if len(ctx.lastpath) > 0 && (ctx.lastpath == path) {
		ctx.underlyingStream.Seek(0, 0)
	} else {
		var err error
		ctx.underlyingStream, err = os.Open(path)
		ctx.lastpath = path
		if err != nil {
			log.Println(err)
			return false
		}
	}
	ctx.rc = bufio.NewReader(ctx.underlyingStream)
	ctx.line_number = 1
	return true
}

func (ctx *countContext) teardown() {
	ctx.underlyingStream.Close()
}

// consume - conditionally consume an expected byte sequence
func (ctx *countContext) consume (expect []byte) bool {
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
	if ctx.last_char_was_newline {
		ctx.line_number++
	}
	if c == '\n' {
		ctx.last_char_was_newline = true
	} else {
		ctx.last_char_was_newline = false
	}
	return c, err
}

// Consume the remainder of a line, updating the line counter
func (ctx *countContext) munchline() ([]byte, error) {
	line, err := ctx.rc.ReadBytes('\n')
	if err != nil {
		ctx.line_number++
	}
	return line, err
}

func isspace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f'
}

// hashbang - hunt for a specified string in the first line of an executable
func hashbang(ctx *countContext, path string, langname string) bool {
	fi, err := os.Stat(path)
	// If it's not executable by somebody, don't read for hashbang
	if err != nil && (fi.Mode() & 01111) == 0 {
		return false
	}
	ctx.Setup(path)
	s, err := ctx.rc.ReadString('\n')
	return err == nil && strings.HasPrefix(s, "#!") && strings.Contains(s, langname)
}

// C line counting algorithm by David A. Wheeler; Go code by ESR.

// c_family_counter - Count the SLOC in a C-family source file
//
// C++ headers get counted as C. This can only be fixed in postprocessing
// by noticing that there are no files with a C extension in the tree.
//
// Another minor issue is that it's possible for the antecedents in Lex rules
// to look like C comment starts. In theory we could fix this by requiring Lex
// files to contain %%.
func c_family_counter(ctx *countContext, path string, syntax cLike) uint {
	/* Types of comments: */
	const BLOCK_COMMENT = 0
	const TRAILING_COMMENT = 1

	var sloc uint = 0
	var mode int = NORMAL            /* NORMAL, INSTRING, or INCOMMENT */
	var comment_type int             /* BLOCK_COMMENT or TRAILING_COMMENT */
	var startline uint

	ctx.Setup(path)
	defer ctx.teardown()

	for {
		c, err := ctx.getachar()
		if err == io.EOF {
			break
		}

		if mode == NORMAL {
			if c == '"' {
				ctx.nonblank = true
				mode = INSTRING
				startline = ctx.line_number
			} else if c == '\'' {
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
			} else if (c == syntax.commentleader[0]) && ctx.ispeek(syntax.commentleader[1]) {
				c, err = ctx.getachar()
				mode = INCOMMENT
				comment_type = BLOCK_COMMENT
				startline = ctx.line_number
			} else if (syntax.eolcomment != "") && (c == syntax.eolcomment[0]) && ctx.ispeek(syntax.eolcomment[1]) {
				c, err = ctx.getachar()
				mode = INCOMMENT
				comment_type = TRAILING_COMMENT
				startline = ctx.line_number
			} else if !isspace(c) {
				ctx.nonblank = true
			}
		} else if mode == INSTRING {
			/*
		        We only count string lines with non-whitespace --
		        this is to gracefully handle syntactically invalid
			programs.
			You could argue that multiline strings with
			whitespace are still executable and should be
			counted.
			 */
			if !isspace(c) {
				ctx.nonblank = true
			}
			if c == '"' {
				mode = NORMAL
			} else if (c == '\\') && (ctx.ispeek('"') || ctx.ispeek('\\')) {
				c, err = ctx.getachar()
			} else if (c == '\\') && ctx.ispeek('\n') {
				c, err = ctx.getachar()
			} else if (c == '\n') {
				/*
                                We found a bare newline in a string without
				preceding backslash.
                                */
				log.Printf("WARNING - newline in string, line %d, file %s\n", ctx.line_number, path)

				/*
                                We COULD warn & reset mode to
                                "Normal", but lots of code does this,
                                so we'll just depend on the warning
                                for ending the program in a string to
                                catch syntactically erroneous
                                programs.
                                */
			}
		} else { /* INCOMMENT mode */
			if (c == '\n') && (comment_type == TRAILING_COMMENT) {
				mode = NORMAL
			}
			if (comment_type == BLOCK_COMMENT) && (c == syntax.commenttrailer[0]) && ctx.ispeek(syntax.commenttrailer[1]) {
				c, err = ctx.getachar()
				mode = NORMAL
			}
		}
		if c == '\n' {
			if ctx.nonblank {
				sloc++
			}
			ctx.nonblank = false
		}
	}
	/* We're done with the file.  Handle EOF-without-EOL. */
	if ctx.nonblank {
		sloc++
	}
	ctx.nonblank = false
	if (mode == INCOMMENT) && (comment_type == TRAILING_COMMENT) {
		mode = NORMAL
	}

	if mode == INCOMMENT {
		log.Printf("\"%s\", line %d: ERROR - terminated in comment beginning here\n",
			path, startline)
	} else if mode == INSTRING {
		log.Printf("\"%s\", line %d: ERROR - terminated in string beginning here\n",
			path, startline)
	}

	return sloc
}

// genericCounter - count SLOC in a generic language.
func genericCounter(ctx *countContext, path string, eolcomment string) uint {
	var sloc uint = 0

	ctx.Setup(path)
	defer ctx.teardown()

	for {
		line, err := ctx.munchline()
		if err == io.EOF {
			break
		} else if err != nil {
			panic(err)
		}
		i := bytes.Index(line, []byte(eolcomment))
		if i > -1 {
			line = line[:i]
		}
		line = bytes.Trim(line, " \t\r\n")
		if len(line) > 0 {
			sloc++
		}
	}

	return sloc
}

func pythonCounter(ctx *countContext, path string) uint {
	var sloc uint = 0
	var isintriple bool	// A triple-quote is in effect.
	var isincomment bool	// We are in a multiline (triple-quoted) comment.

	ctx.Setup(path)
	defer ctx.teardown()

	const dt = "\"\"\""
	const st = "'''"
	dtriple, err := regexp.Compile(dt + "." + dt)
	if err != nil {
		panic(err)
	}
	striple, err := regexp.Compile(st + "." + st)
	if err != nil {
		panic(err)
	}
	dlonely, err := regexp.Compile("^[ \t]*\"[^\"]+\"")
	if err != nil {
		panic(err)
	}
	slonely, err := regexp.Compile("^[ \t]*'[^']+'")
	if err != nil {
		panic(err)
	}
	strailer, err := regexp.Compile(".*" + st)
	if err != nil {
		panic(err)
	}
	dtrailer, err := regexp.Compile(".*" + dt)
	if err != nil {
		panic(err)
	}

	triple_boundary := func(line []byte) bool {return bytes.Contains(line, []byte(dt)) || bytes.Contains(line, []byte(st))}
	for {
		line, err := ctx.munchline()
		if err == io.EOF {
			break
		} else if err != nil {
			panic(err)
		}

		// Delete trailing comments
		i := bytes.Index(line, []byte("#"))
		if i > -1 {
			line = line[:i]
		}

		if !isintriple {  // Normal case:
			// Ignore triple-quotes that begin & end on the line.
			line = dtriple.ReplaceAllLiteral(line, []byte(""))
			line = striple.ReplaceAllLiteral(line, []byte(""))
			// Delete lonely strings starting on BOL.
			line = dlonely.ReplaceAllLiteral(line, []byte(""))
			line = slonely.ReplaceAllLiteral(line, []byte(""))
			// Delete trailing comments
			i := bytes.Index(line, []byte("#"))
			if i > -1 {
				line = line[:i]
			}
			// Does multiline triple-quote begin here?
			if triple_boundary(line) {
		    		isintriple = true;
				line = bytes.Trim(line, " \t\r\n")
				// It's a comment if at BOL.
				if bytes.HasPrefix(line, []byte(dt)) || bytes.HasPrefix(line, []byte(st)){
					isincomment = true
				}
			}
		} else {  // we ARE in a triple.
			if triple_boundary(line) {
				if isincomment {
					// Delete text if it's a comment (not if data)
					line = dtrailer.ReplaceAllLiteral(line, []byte(""))
					line = strailer.ReplaceAllLiteral(line, []byte(""))
				} else {
					// Leave something there to count.
					line = dtrailer.ReplaceAllLiteral(line, []byte("x"))
					line = strailer.ReplaceAllLiteral(line, []byte("x"))
				}
				// But wait!  Another triple might
				// start on this line!  (see
				// Python-1.5.2/Tools/freeze/makefreeze.py
				// for an example)
				if triple_boundary(line) {
					// It did!  No change in state!
				} else {
					isintriple = false
					isincomment = false
				}
			}
		}
		if !isincomment && len(line) > 0 {
			sloc++
		}
	}

	return sloc
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
func perlCounter(ctx *countContext, path string) uint {
	var sloc uint = 0
	var heredoc string
	var isinpod bool

	ctx.Setup(path)
	defer ctx.teardown()

	for {
		line, err := ctx.munchline()
		if err == io.EOF {
			break
		} else if err != nil {
			panic(err)
		}

		// Delete trailing comments
		i := bytes.Index(line, []byte("#"))
		if i > -1 {
			line = line[:i]
		}

		line = bytes.Trim(line, " \t\r\n")
		
		if heredoc != "" && strings.HasPrefix(string(line), heredoc) {
			heredoc = ""    //finished here doc.
		} else if i := bytes.Index(line, []byte("<<")); i > -1 { 
			// Beginning of a here document.
			heredoc = string(bytes.Trim(line[i:], "< \t\"';,"))
		} else if len(heredoc) == 0 && bytes.HasPrefix(line, []byte("=cut")) {
			// Ending a POD?
			if !isinpod {
				log.Printf("\"%s\", %d: cut without pod start\n",
					path, ctx.line_number)
			}
			isinpod = false
			continue  // Don't count the cut command.
		} else if len(heredoc) == 0 && podheader.Match(line) {
			// Starting or continuing a POD?
			// Perlpods can have multiple contents, so
			// it's okay if isinpod == true.  Note that
			// =(space) isn't a POD; library file
			// perl5db.pl does this!
			isinpod = true
		} else if bytes.HasPrefix(line, []byte("__END__")) {
			// Stop processing this file on __END__.
			break
		}
		if !isinpod && len(line) > 0 {
			sloc++
		}
	}

	return sloc
}

// pascalCounter - Handle lanuages like Pascal and Modula 3
func pascalCounter(ctx *countContext, path string, syntax pascalLike) uint {
	var sloc uint = 0
	var mode int = NORMAL              /* NORMAL, or INCOMMENT */
	var startline uint

	ctx.Setup(path)
	defer ctx.teardown()

	for {
		c, err := ctx.getachar()
		if err == io.EOF {
			break
		}

		if mode == NORMAL {
			if syntax.bracketcomments && c == '{' {
				mode = INCOMMENT
			} else if (c == '(') && ctx.ispeek('*') {
				c, err = ctx.getachar()
				mode = INCOMMENT
			} else if !isspace(c) {
				ctx.nonblank = true
			} else if c == '\n' {
				if ctx.nonblank {
					sloc++
				}
				ctx.nonblank = false
			}
		} else { /* INCOMMENT mode */
			if syntax.bracketcomments && c == '}' {
				mode = NORMAL
			} else if (c == '*') && ctx.ispeek(')') {
				c, err = ctx.getachar()
				mode = NORMAL
			}
		}
	}
	/* We're done with the file.  Handle EOF-without-EOL. */
	if ctx.nonblank {
		sloc++
	}
	ctx.nonblank = false

	if mode == INCOMMENT {
		log.Printf("\"%s\", line %d: ERROR - terminated in comment beginning here.\n",
			path, startline)
	} else if mode == INSTRING {
		log.Printf("\"%s\", line %d: ERROR - terminated in string beginning here.\n",
			path, startline)
	}

	return sloc
}

func fortranCounter(ctx *countContext, path string, syntax fortranLike) uint {
	var sloc uint

	ctx.Setup(path)
	defer ctx.teardown()

	for {
		line, err := ctx.munchline()
		if err != nil {
			break
		}
		if !(syntax.comment.Match(line) && !syntax.nocomment.Match(line)) {
			sloc++
		}
	}
	return sloc
}

// Generic - recognize lots of languages with generic syntax
func Generic(ctx *countContext, path string) SourceStat {
	var stat SourceStat

	for i := range cLikes {
		lang := cLikes[i]
		if strings.HasSuffix(path, lang.extension) {
			stat.Language = lang.name
			stat.SLOC = c_family_counter(ctx, path, lang)
			break
		}
	}

	if strings.HasSuffix(path, ".py") || hashbang(ctx, path, "python") {
		stat.Language = "python"
		stat.SLOC = pythonCounter(ctx, path)
	}
		
	if strings.HasSuffix(path, ".pl") || hashbang(ctx, path, "perl") {
		stat.Language = "perl"
		stat.SLOC = perlCounter(ctx, path)
	}
		
	if filepath.Base(path) == "wscript" {
		stat.Language = "waf"
		stat.SLOC = pythonCounter(ctx, path)
	}
		
	for i := range scriptingLanguages {
		lang := scriptingLanguages[i]
		if strings.HasSuffix(path, lang.suffix) || hashbang(ctx, path, lang.hashbang) {
			stat.Language = lang.name
			stat.SLOC = genericCounter(ctx, path, "#")
			break
		}
	}

	for i := range genericLanguages {
		lang := genericLanguages[i]
		if strings.HasSuffix(path, lang.suffix) {
			stat.Language = lang.name
			stat.SLOC = genericCounter(ctx,	path, lang.eolcomment)
			break
		}
	}

	for i := range pascalLikes {
		lang := pascalLikes[i]
		if strings.HasSuffix(path, lang.suffix) {
			stat.Language = lang.name
			stat.SLOC = pascalCounter(ctx, path, lang)
			break
		}
	}

	for i := range fortranLikes {
		lang := fortranLikes[i]
		if strings.HasSuffix(path, lang.suffix) {
			stat.Language = lang.name
			stat.SLOC = fortranCounter(ctx, path, lang)
			break
		}
	}

	return stat
}

func isDirectory(path string) (bool) {
	fileInfo, err := os.Stat(path)
	return err == nil && fileInfo.IsDir()
}

// filter - winnows out uninteresting paths before handing them to process
func filter(path string, info os.FileInfo, err error) error {
	for i := range neverInterestingByPrefix {
		if strings.HasPrefix(path, neverInterestingByPrefix[i]) {
			return err
		}
	}
	for i := range neverInterestingByInfix {
		if strings.Contains(path, neverInterestingByInfix[i]) {
			if isDirectory(path) {
				return filepath.SkipDir
			} else {
				return err
			}
		}
	}
	for i := range neverInterestingBySuffix {
		if strings.HasSuffix(path, neverInterestingBySuffix[i]) {
			return err
		}
	}
	for i := range neverInterestingByBasename {
		if filepath.Base(path) == neverInterestingByBasename[i] {
			return err
		}
	}
	for i := range exclusions {
		if path == exclusions[i] || strings.HasPrefix(path, exclusions[i]+"/") {
			return err
		}
	}

	/* has to come after the infix check for directory */
	if isDirectory(path) {
		return err
	}

	/* toss generated Makefiles */
	if filepath.Base(path) == "Makefile" {
		if _, err := os.Stat(path + ".in"); err == nil {
			return err
		}
	}

	// Now the real work gets done
	ctx := new(countContext)
	st := Generic(ctx, path)
	st.Path = path
	pipeline <- st

	return err
}

type countRecord struct {
	language string
	linecount uint
	filecount uint
}

func reportCocomo(sloc uint) {
	const TIME_MULT = 2.4
	const TIME_EXP = 1.05
	const SCHED_MULT = 2.5
	const SCHED_EXP = 0.38
	const SALARY = 60384	// From payscale.com, late 2016
	const OVERHEAD = 2.40
	fmt.Printf("Total Physical Source Lines of Code (SLOC)                = %d\n", sloc)
	person_months := TIME_MULT * math.Pow(float64(sloc)/1000, TIME_EXP)
	fmt.Printf("Development Effort Estimate, Person-Years (Person-Months) = %2.2f (%2.2f)\n", person_months / 12, person_months) 
	fmt.Printf(" (Basic COCOMO model, Person-Months = %2.2f * (KSLOC**%2.2f))\n", TIME_MULT, TIME_EXP)
	sched_months := SCHED_MULT * math.Pow(person_months, SCHED_EXP)
	fmt.Printf("Schedule Estimate, Years (Months)                         = %2.2f (%2.2f)\n", sched_months/12, sched_months)
	fmt.Printf(" (Basic COCOMO model, Months = %2.2f * (person-months**%2.2f))\n", SCHED_MULT, SCHED_EXP)
	fmt.Printf("Estimated Average Number of Developers (Effort/Schedule)  = %2.2f\n", person_months / sched_months)
	fmt.Printf("Total Estimated Cost to Develop                           = $%d\n", int(SALARY * (person_months / 12) * OVERHEAD))
	fmt.Printf(" (average salary = $%d/year, overhead = %2.2f).\n", SALARY, OVERHEAD)
}

func list_languages() {
	var names []string
	var lastlang string
	for i := range cLikes {
		lang := cLikes[i].name
		if lang != lastlang {
			names = append(names, lang)
			lastlang = lang
		}
	}

	for i := range scriptingLanguages {
		lang := scriptingLanguages[i].name
		if lang != lastlang {
			names = append(names, lang)
			lastlang = lang
		}
	}

	for i := range genericLanguages {
		lang := genericLanguages[i].name
		if lang != lastlang {
			names = append(names, lang)
			lastlang = lang
		}
	}

	for i := range pascalLikes {
		lang := pascalLikes[i].name
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
	sort.Strings(names)
	fmt.Printf("%s\n", names)
}

type sortable []countRecord 
func (a sortable) Len() int {return len(a)}
func (a sortable) Swap(i int, j int)  { a[i], a[j] = a[j], a[i] }
func (a sortable) Less(i, j int) bool { return -a[i].linecount < -a[j].linecount }

func main() {
	var individual bool
	var unclassified bool
	var list bool
	var cocomo bool
	excludePtr := flag.String("x", "",
		"paths and directories to exclude")
	flag.BoolVar(&individual, "i", false,
		"list counts and types for individual files")
	flag.BoolVar(&unclassified, "u", false,
		"list unclassified files")
	flag.BoolVar(&cocomo, "c", false,
		"report Cocomo-model estimation")
	flag.BoolVar(&list, "l", false,
		"list supported languages and exit")
	flag.Parse()

	if list {
		list_languages()
		return
	}

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
	
	exclusions = strings.Split(*excludePtr, ",")
	roots := flag.Args()

	go func() {
		for i := range roots {
			filepath.Walk(roots[i], filter)
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

		if individual {
			if !unclassified && st.SLOC > 0 {
				fmt.Printf("%s %d %s\n",
					st.Path, st.SLOC, st.Language)
			} else if unclassified {
				// Not a recognized source type,
				// nor anything we know to discard
				fmt.Println(st.Path)
			}
			continue
		}

		if st.SLOC > 0 {
			var tmp = counts[st.Language]
			tmp.language = st.Language
			tmp.linecount += st.SLOC
			tmp.filecount++
			counts[st.Language] = tmp
			totals.linecount += st.SLOC
			totals.filecount++
		}
	}

	if individual {
		return
	}

	// C headers may get reassigned based on what other languages
	// are present in the tree
	if counts["c-header"].linecount > 0 {
		for i := range cHeaderPriority {
			if counts[cHeaderPriority[i]].linecount > 0 {
				var tmp = counts[cHeaderPriority[i]]
				tmp.linecount += counts["c-header"].linecount
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
		fmt.Printf("%-12s %7d (%2.2f%%) in %d files\n",
			r.language,
			r.linecount,
			float64(r.linecount) * 100.0 / float64(totals.linecount),
			r.filecount)
	}

	if (cocomo) {
		reportCocomo(totals.linecount)
	}
}

// end
