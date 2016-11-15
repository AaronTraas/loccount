package lang

/*
 * Algorithm by David A. Wheeler; Go code by ESR.
 */

import "os"
import "strings"
import "io"
import "bufio"
import "log"
import "loccount/stats"

/* Types of comments: */
const ANSIC_STYLE = 0
const CPP_STYLE = 1

var warn_embedded_newlines = false

/*
 * sloc_count - Count the SLOC in a C file
 */
func sloc_count(stream *os.File) uint {
	var sloc uint = 0
	var sawchar bool = false           /* Did you see a char on this line? */
	var mode int = NORMAL              /* NORMAL, INSTRING, or INCOMMENT */
	var comment_type int = ANSIC_STYLE /* ANSIC_STYLE or CPP_STYLE */
	rc = bufio.NewReader(stream)

	/*
        The following implements a state machine with transitions; the
        main state is "mode" and "comment_type", the transitions are
	triggered by characters input.
	*/

	for {
		c, err := getachar()
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
				c, err = getachar()
				if c == '\\' {
					c, err = getachar()
				}
				for {
					c, err = getachar()
					if (c == '\'') || (c == '\n') || (err == io.EOF) {
						break
					}
				}
			} else if (c == '/') && ispeek('*') {
				c, err = getachar()
				mode = INCOMMENT
				comment_type = ANSIC_STYLE
			} else if (c == '/') && ispeek('/') {
				c, err = getachar()
				mode = INCOMMENT
				comment_type = CPP_STYLE
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
			} else if (c == '\\') && (ispeek('"') || ispeek('\\')) {
				c, err = getachar()
			} else if (c == '\\') && ispeek('\n') {
				c, err = getachar()
			} else if (c == '\n') && warn_embedded_newlines {
				/*
                                We found a bare newline in a string without
				preceding backslash.
                                */
				log.Printf("c_count WARNING - newline in string, line %ld, file %s\n", line_number, stream.Name())

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
			if (c == '\n') && (comment_type == CPP_STYLE) {
				mode = NORMAL
			}
			if (comment_type == ANSIC_STYLE) && (c == '*') && ispeek('/') {
				c, err = getachar()
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
	if (mode == INCOMMENT) && (comment_type == CPP_STYLE) {
		mode = NORMAL
	}

	if mode == INCOMMENT {
		log.Printf("c_count ERROR - terminated in comment in %s\n", stream.Name())
	} else if mode == INSTRING {
		log.Printf("c_count ERROR - terminated in string in %s\n", stream.Name())
	}

	return sloc
}

/*
 * C - recognize C files and get linecounts from them.
 */
func C(path string) stats.SourceStat {
	var stat stats.SourceStat
	if strings.HasSuffix(path, ".c") || strings.HasSuffix(path, ".h") {
		stat.Language = "C"
		f, err := os.Open(path)
		if err != nil {
			log.Println(err)
			return stat
		}
		defer f.Close()
		line_number = 1
		stat.SLOC = sloc_count(f)
	}
	return stat
}
