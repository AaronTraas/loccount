package main

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

type SourceStat struct {
	Path string
	Language string
	SLOC uint
}

var exclusions []string
var pipeline chan SourceStat

// Data tables driving the recognition and counting of classes of languages.

type cLike struct {
	language string
	extension string
	commentleader string
	commenttrailer string
	eolcomment string
}
var cLikes []cLike

// We get to specify a set of possible string delimiters (normally
// a singleton string containing single or double quote, or a doubleton
// containing both). We also get to specify a comment leader.
type scriptingLanguage struct {
	name string
	suffix string
	hashbang string
	stringdelims string
	eolcomment string
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

var neverInterestingByPrefix []string
var neverInterestingByInfix []string
var neverInterestingBySuffix []string
var neverInterestingByBasename []string

var cHeaderPriority []string

func init() {
	cLikes = []cLike{
		{"ansic", ".c", "/*", "*/", "//"},
		{"c-header", ".h", "/*", "*/", "//"},
		{"yacc", ".y", "/*", "*/", "//"},
		{"lex", ".l", "/*", "*/", "//"},
		{"c++", ".cpp", "/*", "*/", "//"},
		{"c++", ".cxx", "/*", "*/", "//"},
		{"obj-c", ".m", "/*", "*/", "//"},
		{"c#", ".cs", "/*", "*/", "//"},
		{"php", ".php", "/*", "*/", "//"},
		{"go", ".go", "/*", "*/", "//"},
		{"sql", ".sql", "/*", "*/", "--"},
		{"haskell", ".hs", "{-", "-}", "--"},
		{"autotools", "config.h.in", "/*", "*/", "//"},
	}
	scriptingLanguages = []scriptingLanguage{
		// First line doesn't look like it handles Python
		// multiline string literals, but it actually does.
		// The delimiters for them are ''' """ which get seen
		// as an empty string followed by a string delimiter,
		// or the reverse of that. Interior lines of a
		// multiline literal get counted if they contain non-
		// whitespace.
		//
		// This is different from sloccount's behavior, which
		// doesn't count multiline literals if they start at the
		// beginning of a line (e.g. as in Python header comments).
		{"python", ".py", "python", "'\"", "#"},
		{"waf", "wscript", "waf", "'\"", "#"},
		{"perl", ".pl", "perl", "'\"", "#"},
		{"tcl", ".tcl", "tcl", "'\"", "#"},	/* must be before sh */
		{"csh", ".csh", "csh", "'\"", "#"},
		{"shell", ".sh", "sh", "'\"", "#"},
		{"ruby", ".rb", "ruby", "'\"", "#"},
		{"awk", ".awk", "awk", "'\"", "#"},
		{"sed", ".sed", "sed", "'\"", "#"},
	}
	genericLanguages = []genericLanguage{
		{"ada", ".ada", "--"},
		{"makefile", ".mk", "#"},
		{"makefile", "Makefile", "#"},
		{"makefile", "makefile", "#"},
		{"makefile", "Imakefile", "#"},
		{"m4", ".m4", "#"},
		{"lisp", ".lisp", ";"},
		{"lisp", ".lsp", ";"},	// XLISP
		{"lisp", ".cl", ";"},	// Common Lisp
		{"cobol", "CBL", "*"},
		{"cobol", "cbl", "*"},
		{"eiffel", ".e", "--"},
		{"sather", ".sa", "--"},
		{"lua", ".lua", "--"},
		{"clu", ".clu", "%"},
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
	}

	neverInterestingByPrefix = []string{"."}
	neverInterestingByInfix = []string{".so.", "/."}
	neverInterestingBySuffix = []string{"~",
		".a", ".la", ".o", ".so",
		".gif", ".jpg", ".jpeg", ".ico",
		".pyc", ".pyo",
	}
	neverInterestingByBasename = []string{
		"configure", "autom4te.cache", "config.log", "config.status",
	}
	cHeaderPriority = []string{"C", "C++", "Objective-C"}
}

// Generic machinery for walking source text to count lines

/* Modes */
const NORMAL = 0
const INSTRING = 1
const INCOMMENT = 2

type countContext struct {
	line_number uint
	last_char_was_newline bool
	underlyingStream *os.File
	rc *bufio.Reader
	lastpath string
}

func peek(ctx *countContext) byte {
	bytes, err := ctx.rc.Peek(1)
	if err != nil {
		panic("error while peeking")
	}
	return bytes[0]
}

func ispeek(ctx *countContext, c byte) bool {
	if c == peek(ctx) {
		return true
	}
	return false
}

// getachar - Get one character, tracking line number
func getachar(ctx *countContext) (byte, error) {
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

func isspace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f'
}

func contains(s string, c byte) bool {
	for i := range s {
		if s[i] == c {
			return true
		}
	}
	return false
}

// The purpose of the following two functions is to set up context
// so that we never have to open a source file more than once.

func bufferSetup(ctx *countContext, path string) bool {
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

func bufferTeardown(ctx *countContext) {
	ctx.underlyingStream.Close()
}

// hashbang - hunt for a specified string in the first line of an executable
func hashbang(ctx *countContext, path string, langname string) bool {
	fi, err := os.Stat(path)
	// If it's not executable by somebody, don't read for hashbang
	if err != nil && (fi.Mode() & 01111) == 0 {
		return false
	}
	bufferSetup(ctx, path)
	s, err := ctx.rc.ReadString('\n')
	return err == nil && strings.HasPrefix(s, "#!") && strings.Contains(s, langname)
}

// C line counting algorithm by David A. Wheeler; Go code by ESR.

/* Types of comments: */
const BLOCK_COMMENT = 0
const TRAILING_COMMENT = 1

// c_family_counter - Count the SLOC in a C-family source file
func c_family_counter(ctx *countContext, path string, syntax cLike) uint {
	var sloc uint = 0
	var sawchar bool = false           /* Did you see a char on this line? */
	var mode int = NORMAL              /* NORMAL, INSTRING, or INCOMMENT */
	var comment_type int = BLOCK_COMMENT /* BLOCK_COMMENT or TRAILING_COMMENT */

	/*
        The following implements a state machine with transitions; the
        main state is "mode" and "comment_type", the transitions are
	triggered by characters input.
	*/

	for {
		c, err := getachar(ctx)
		if err == io.EOF {
			break
		}

		if mode == NORMAL {
			if c == '"' {
				sawchar = true
				mode = INSTRING
			} else if c == '\'' {
				/* Consume single-character 'xxxx' values */
				sawchar = true
				c, err = getachar(ctx)
				if c == '\\' {
					c, err = getachar(ctx)
				}
				for {
					c, err = getachar(ctx)
					if (c == '\'') || (c == '\n') || (err == io.EOF) {
						break
					}
				}
			} else if (c == syntax.commentleader[0]) && ispeek(ctx, syntax.commentleader[1]) {
				c, err = getachar(ctx)
				mode = INCOMMENT
				comment_type = BLOCK_COMMENT
			} else if (c == syntax.eolcomment[0]) && ispeek(ctx, syntax.eolcomment[1]) {
				c, err = getachar(ctx)
				mode = INCOMMENT
				comment_type = TRAILING_COMMENT
			} else if !isspace(c) {
				sawchar = true
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
				sawchar = true
			}
			if c == '"' {
				mode = NORMAL
			} else if (c == '\\') && (ispeek(ctx, '"') || ispeek(ctx, '\\')) {
				c, err = getachar(ctx)
			} else if (c == '\\') && ispeek(ctx, '\n') {
				c, err = getachar(ctx)
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
			if (comment_type == BLOCK_COMMENT) && (c == syntax.commenttrailer[0]) && ispeek(ctx, syntax.commenttrailer[1]) {
				c, err = getachar(ctx)
				mode = NORMAL
			}
		}
		if c == '\n' {
			if sawchar {
				sloc++
			}
			sawchar = false
		}
	}
	/* We're done with the file.  Handle EOF-without-EOL. */
	if sawchar {
		sloc++
	}
	sawchar = false
	if (mode == INCOMMENT) && (comment_type == TRAILING_COMMENT) {
		mode = NORMAL
	}

	if mode == INCOMMENT {
		log.Printf("ERROR - terminated in comment in %s\n", path)
	} else if mode == INSTRING {
		log.Printf("ERROR - terminated in string in %s\n", path)
	}

	return sloc
}

// C - recognize files from C-family languages and get linecounts from them.
//
// C++ headers get counted as C. This can only be fixed in postprocessing
// by noticing that there are no files with a C extension in the tree.
//
// Another minor issue is that it's possible for the antecedents in Lex rules
// to look like C comment starts. In theory we could fix this by requiring Lex
// files to contain %%.
func C(ctx *countContext, path string) SourceStat {
	var stat SourceStat
	for i := range cLikes {
		lang := cLikes[i]
		if strings.HasSuffix(path, lang.extension) {
			stat.Language = lang.language
			bufferSetup(ctx, path)
			defer bufferTeardown(ctx)
			stat.SLOC = c_family_counter(ctx, path, lang)
		}
	}
	return stat
}

// genericCounter - count SLOC in a generic language.
func genericCounter(ctx *countContext, path string, stringdelims string, eolcomment string) uint {
	var sloc uint = 0
	var sawchar bool = false           /* Did you see a char on this line? */
	var mode int = NORMAL              /* NORMAL, INSTRING, or INCOMMENT */
	var delimseen byte                 /* what string delimiter? */

	bufferSetup(ctx, path)
	defer bufferTeardown(ctx)
	
	for {
		c, err := getachar(ctx)
		if err == io.EOF {
			break
		}

		if mode == NORMAL {
			if contains(stringdelims, c) {
				sawchar = true
				delimseen = c
				mode = INSTRING
			} else if (c == eolcomment[0]) {
				if len(eolcomment) == 1 {
					mode = INCOMMENT
				} else {
					c, err = getachar(ctx)
					if err == nil && c == eolcomment[1] {
						mode = INCOMMENT
					}
				}
			} else if !isspace(c) {
				sawchar = true
			}
		} else if mode == INSTRING {
			if c == delimseen {
				mode = NORMAL
			} else if !isspace(c) {
				sawchar = true
			}
		} else { /* INCOMMENT mode */
			if (c == '\n') {
				mode = NORMAL
			}
		}
		if c == '\n' {
			if sawchar {
				sloc++
			}
			sawchar = false
		}
	}

	/* We're done with the file.  Handle EOF-without-EOL. */
	if sawchar {
		sloc++
	}
	sawchar = false

	if mode == INCOMMENT {
		log.Printf("ERROR - terminated in comment in %s\n", path)
	} else if mode == INSTRING {
		log.Printf("ERROR - terminated in string in %s\n", path)
	}

	return sloc
}

// pascalCounter - Handle lanuages like Pascal and Modula 3
func pascalCounter(ctx *countContext, path string, syntax pascalLike) uint {
	var sloc uint = 0
	var sawchar bool = false           /* Did you see a char on this line? */
	var mode int = NORMAL              /* NORMAL, or INCOMMENT */

	bufferSetup(ctx, path)
	defer bufferTeardown(ctx)

	for {
		c, err := getachar(ctx)
		if err == io.EOF {
			break
		}

		if mode == NORMAL {
			if syntax.bracketcomments && c == '{' {
				mode = INCOMMENT
			} else if (c == '(') && ispeek(ctx, '*') {
				c, err = getachar(ctx)
				mode = INCOMMENT
			} else if !isspace(c) {
				sawchar = true
			} else if c == '\n' {
				if sawchar {
					sloc++
				}
				sawchar = false
			}
		} else { /* INCOMMENT mode */
			if syntax.bracketcomments && c == '}' {
				mode = NORMAL
			} else if (c == '*') && ispeek(ctx, ')') {
				c, err = getachar(ctx)
				mode = NORMAL
			}
		}
	}
	/* We're done with the file.  Handle EOF-without-EOL. */
	if sawchar {
		sloc++
	}
	sawchar = false

	if mode == INCOMMENT {
		log.Printf("ERROR - terminated in comment in %s\n", path)
	} else if mode == INSTRING {
		log.Printf("ERROR - terminated in string in %s\n", path)
	}

	return sloc
}

// Generic - recognize lots of languages with generic syntax
func Generic(ctx *countContext, path string) SourceStat {
	var stat SourceStat

	for i := range scriptingLanguages {
		lang := scriptingLanguages[i]
		if strings.HasSuffix(path, lang.suffix) || hashbang(ctx, path, lang.hashbang) {
			stat.Language = lang.name
			stat.SLOC = genericCounter(ctx,
				path, lang.stringdelims, lang.eolcomment)
			break
		}
	}

	for i := range genericLanguages {
		lang := genericLanguages[i]
		if strings.HasSuffix(path, lang.suffix) {
			stat.Language = lang.name
			stat.SLOC = genericCounter(ctx,
				path, "", lang.eolcomment)
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

	return stat
}

func Fortran90(ctx *countContext, path string) SourceStat {
	var stat SourceStat

	if !strings.HasSuffix(path, ".f90") {
		return stat
	}
	
	bufferSetup(ctx, path)
	defer bufferTeardown(ctx)

	re1, err := regexp.Compile("^([c*!]|[ \t]+!|[ \t]*$)")
	if err != nil {
		panic("unexpected failure while building 90 comment analyzer")
	}
	re2, err := regexp.Compile("^[c*!](hpf|omp)[$]")
	if err != nil {
		panic("unexpected failure while building 90 no-comment analyzer")
	}
	for {
		line, err := ctx.rc.ReadBytes('\n')
		if err != nil {
			break
		}
		if !(re1.Match(line) && !re2.Match(line)) {
			stat.SLOC++
		}
	}
	if stat.SLOC > 0 {
		stat.Language = "Fortran90"
	}
	return stat
}

// process - gather file statistics and suff them in the pipeline
func process(path string) {
	handlerList := []func(*countContext, string) SourceStat {
		C,          /* also C++ */
		Generic,    /* Python, Perl, Ruby, shell, waf, Ada... */
		Fortran90,  /* Fortran90 */
	}
	var st SourceStat
	ctx := new(countContext)
	for i := range handlerList {
		st = handlerList[i](ctx, path)
		if st.SLOC > 0 {
			break
		}
	}
	st.Path = path
	pipeline <- st
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
	
	process(path)
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

type sortable []countRecord 
func (a sortable) Len() int {return len(a)}
func (a sortable) Swap(i int, j int)  { a[i], a[j] = a[j], a[i] }
func (a sortable) Less(i, j int) bool { return -a[i].linecount < -a[j].linecount }

func main() {
	var individual bool
	var unclassified bool
	var cocomo bool
	excludePtr := flag.String("x", "",
		"paths and directories to exclude")
	flag.BoolVar(&individual, "i", false,
		"list counts and types for individual files")
	flag.BoolVar(&unclassified, "u", false,
		"list unclassified files")
	flag.BoolVar(&cocomo, "c", false,
		"report Cocomo-model estimation")
	flag.Parse()

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
