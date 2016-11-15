package lang

/*
 * Algorithm by David A. Wheeler; Go code by ESR.
 */

import "strings"
import "io"
import "log"
import "loccount/stats"

/* Types of comments: */
const ANSIC_STYLE = 0
const CPP_STYLE = 1

var warn_embedded_newlines = false

/*
 * sloc_count - Count the SLOC in a C or C++ file
 */
func sloc_count(path string) uint {
	var sloc uint = 0
	var sawchar bool = false           /* Did you see a char on this line? */
	var mode int = NORMAL              /* NORMAL, INSTRING, or INCOMMENT */
	var comment_type int = ANSIC_STYLE /* ANSIC_STYLE or CPP_STYLE */

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
				log.Printf("WARNING - newline in string, line %ld, file %s\n", line_number, path)

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
		log.Printf("ERROR - terminated in comment in %s\n", path)
	} else if mode == INSTRING {
		log.Printf("ERROR - terminated in string in %s\n", path)
	}

	return sloc
}

/*
 * C - recognize files from C/C++/Go and get linecounts from them.
 *
 * C++ headers get counted as C. This can only be fixed in postprocessing
 * by noticing that there are no files with a C extension in the tree.
 */
func C(path string) stats.SourceStat {
	var stat stats.SourceStat
	if strings.HasSuffix(path, ".c") || strings.HasSuffix(path, ".h") {
		stat.Language = "C"
		bufferSetup(path)
		defer bufferTeardown()
		stat.SLOC = sloc_count(path)
	}
	if strings.HasSuffix(path, ".cpp") || strings.HasSuffix(path, ".cxx") {
		stat.Language = "C++"
		bufferSetup(path)
		defer bufferTeardown()
		stat.SLOC = sloc_count(path)
	}
	if strings.HasSuffix(path, ".go") {
		stat.Language = "Go"
		bufferSetup(path)
		defer bufferTeardown()
		stat.SLOC = sloc_count(path)
	}
	return stat
}
