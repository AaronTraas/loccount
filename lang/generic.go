package lang

import "os"
import "log"
import "io"
import "strings"
import "bufio"

/* Modes */
const NORMAL = 0
const INSTRING = 1
const INCOMMENT = 2

/* Globals */
var line_number uint
var last_char_was_newline bool = false
var underlyingStream *os.File
var rc *bufio.Reader

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
	if underlyingStream == nil {
		var err error
		underlyingStream, err = os.Open(path)
		if err != nil {
			log.Println(err)
			return false
		}
	} else {
		underlyingStream.Seek(0, 0)
	}
	rc = bufio.NewReader(underlyingStream)
	line_number = 1
	return true
}

func bufferTeardown() {
	underlyingStream.Close()
}

// sniff - hunt for a specified string in the first line of a file
func sniff(path string, langname string) bool {
	bufferSetup(path)
	s, err := rc.ReadString('\n')
	return err != nil && strings.Contains(s, langname)
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
			} else if (c == commentleader) {
				c, err = getachar()
				mode = INCOMMENT
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
// end
