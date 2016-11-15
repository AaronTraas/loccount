package lang

import "os"
import "log"
import "io"
import "strings"
import "bufio"

import "loccount/stats"

/* Modes */
const NORMAL = 0
const INSTRING = 1
const INCOMMENT = 2

/* Globals */
var line_number uint
var last_char_was_newline bool = false
var underlyingStream *os.File
var rc *bufio.Reader
var lastpath string

func peek() byte {
	bytes, err := rc.Peek(1)
	if err != nil {
		panic("error while peeking")
	}
	return bytes[0]
}

func ispeek(c byte) bool {
	if c == peek() {
		return true
	}
	return false
}

// getachar - Get one character, tracking line number
func getachar() (byte, error) {
	c, err := rc.ReadByte()
	if err != nil && err != io.EOF {
		panic("error while reading a character")
	}
	if last_char_was_newline {
		line_number++
	}
	if c == '\n' {
		last_char_was_newline = true
	} else {
		last_char_was_newline = false
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

func bufferSetup(path string) bool {
	if len(lastpath) > 0 && (lastpath == path) {
		underlyingStream.Seek(0, 0)
	} else {
		var err error
		underlyingStream, err = os.Open(path)
		lastpath = path
		if err != nil {
			log.Println(err)
			return false
		}
	}
	rc = bufio.NewReader(underlyingStream)
	line_number = 1
	return true
}

func bufferTeardown() {
	underlyingStream.Close()
}

// hashbang - hunt for a specified string in the first line of a file
func hashbang(path string, langname string) bool {
	bufferSetup(path)
	s, err := rc.ReadString('\n')
	return err != nil && strings.HasPrefix(s, "#!") && strings.Contains(s, langname)
}

// generic_sloc_count - count SLOC in a generic scripting language.
//
// We get to specify a set of possible string delimiters (normally
// a singleton string containing single or double quote, or a doubleton
// containing both). We also get to specify a comment leader.
func generic_sloc_count(path string, stringdelims string, commentleader byte) uint {
	var sloc uint = 0
	var sawchar bool = false           /* Did you see a char on this line? */
	var mode int = NORMAL              /* NORMAL, INSTRING, or INCOMMENT */
	var delimseen byte                 /* what string delimiter? */

	bufferSetup(path)
	defer bufferTeardown()
	
	/*
        The following implements a state machine with transitions; the
        main state is "mode", the transitions are
	triggered by characters input.
	*/

	for {
		c, err := getachar()
		if err == io.EOF {
			break
		}

		if mode == NORMAL {
			if contains(stringdelims, c) {
				sawchar = true
				delimseen = c
				mode = INSTRING
				//log.Printf("normal->string at line %d\n", line_number)
			} else if (c == commentleader) {
				c, err = getachar()
				mode = INCOMMENT
				//log.Printf("normal->comment at line %d\n", line_number)
			} else if !isspace(c) {
				sawchar = true
			}
		} else if mode == INSTRING {
			if c == delimseen {
				//log.Printf("string->normal at line %d\n", line_number)
				mode = NORMAL
			} else if !isspace(c) {
				sawchar = true
			}
		} else { /* INCOMMENT mode */
			if (c == '\n') {
				mode = NORMAL
				//log.Printf("comment->normal at line %d\n", line_number)
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

/*
 * Generic - recognize lots of languages with generic syntax
 */

func Generic(path string) stats.SourceStat {
	var stat stats.SourceStat

	type scriptingLanguage struct {
		name string
		suffix string
		hashbang string
		stringdelims string
		commentleader byte
	}
	scriptingLanguages := []scriptingLanguage{
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
		{"Python", ".py", "python", "'\"", '#'},
		{"waf", "wscript", "waf", "'\"", '#'},
		{"Perl", ".pl", "perl", "'\"", '#'},
		{"shell", ".sh", "shell", "'\"", '#'},
		{"Ruby", ".rb", "ruby", "'\"", '#'},
	}

	for i := range scriptingLanguages {
		lang := scriptingLanguages[i]
		if strings.HasSuffix(path, lang.suffix) || hashbang(path, lang.hashbang) {
			stat.Language = lang.name
			stat.SLOC = generic_sloc_count(path,
				lang.stringdelims, lang.commentleader)
			break
		}
	}

	return stat
}



// end
