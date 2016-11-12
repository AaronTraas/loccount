package main

import "fmt"
import "flag"
import "os"
import "path/filepath"
import "strings" 

var exclusions []string

// process - stub, eventually the statistics gatherer 
func process(path string) {
	fmt.Printf("%s\n", path)
}

// filter - winnows out uninteresting paths before handing them to process
func filter(path string, info os.FileInfo, err error) error {
	//basename := filepath.Base(path)

	/* ignore dotfiles due to version cmtrol systems */
	if strings.Contains(path, "/.") || path[0] == '.' {
		return err
	}
	for i := range exclusions {
		if path == exclusions[i] || strings.HasPrefix(path, exclusions[i] + "/") {
			return err
		}
	}

	process(path)
	return err
}

func main() {
	excludePtr := flag.String("exclude", "", "directories to exclude")
	flag.Parse()

	exclusions = strings.Split(*excludePtr, ",")
	roots := flag.Args()
	for i := range roots {
		filepath.Walk(roots[i], filter)
	}
}
