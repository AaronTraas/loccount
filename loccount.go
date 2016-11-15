package main

import "fmt"
import "flag"
import "os"
import "path/filepath"
import "strings"

import "loccount/lang"
import "loccount/stats"

var exclusions []string
var unclassified bool

// process - stub, eventually the statistics gatherer
func process(path string) {
	handlerList := []func(string) stats.SourceStat {
		lang.C,
		lang.Python,
	}
	var st stats.SourceStat
	for i := range handlerList {
		st = handlerList[i](path)
		if st.SLOC > 0 {
			if !unclassified {
				fmt.Printf("%s %d\n", path, st.SLOC)
			}
			return
		}
	}
	// Not a recognized source type, nor anything we know to discard
	if unclassified {
		fmt.Println(path)
	}
}

func isDirectory(path string) (bool) {
	fileInfo, err := os.Stat(path)
	return err == nil && fileInfo.IsDir()
}




// filter - winnows out uninteresting paths before handing them to process
func filter(path string, info os.FileInfo, err error) error {
	neverInterestingByPrefix := []string{"."}
	neverInterestingByInfix := []string{".so.", "/."}
	neverInterestingBySuffix := []string{"~",
		".a", ".la", ".o", ".so",
		".gif", ".jpg", ".jpeg", ".ico",
		".pyc", ".pyo"}

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
	for i := range exclusions {
		if path == exclusions[i] || strings.HasPrefix(path, exclusions[i]+"/") {
			return err
		}
	}

	/* has to come after the infix check for directory */
	if isDirectory(path) {
		return err
	}
	
	process(path)
	return err
}

func main() {
	excludePtr := flag.String("exclude", "",
		"paths directories to exclude")
	flag.BoolVar(&unclassified, "unclassified", false,
		"list unclassified files")
	flag.Parse()

	exclusions = strings.Split(*excludePtr, ",")
	roots := flag.Args()
	for i := range roots {
		filepath.Walk(roots[i], filter)
	}
}
