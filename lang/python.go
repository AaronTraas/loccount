package lang

import "os"
import "log"
import "strings"
import "loccount/stats"

/*
 * Python - recognize Python files and get linecounts from them.
 */
func Python(path string) stats.SourceStat {
	var stat stats.SourceStat
	if strings.HasSuffix(path, ".py") {
		stat.Language = "Python"
		f, err := os.Open(path)
		if err != nil {
			log.Println(err)
			return stat
		}
		defer f.Close()
		line_number = 1
		// This doesn't look like it handles Python multiline string
		// literals, but it actually does.  The delimiters for them are
		// ''' """ which get seen as an empty string followed by a
		// string delimiter, or the reverse of that. Interior lines
		// of a multiline literal get counted if they contain non-
		// whitespace.
		//
		// This is different fron sloccount's behavior, which
		// doesn't count multiline literals if they start at the
		// beginning of a line (e.g. as in Python header comments).
		stat.SLOC = generic_sloc_count(f, "'\"", '#')
	}
	return stat
}

// end
