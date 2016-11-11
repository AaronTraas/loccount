package main

import "fmt"
import "flag"
import "os"
import "path/filepath"

func linecounter(path string, info os.FileInfo, err error) error {
	fmt.Printf("path: %s\n", path)
	return err
}

func main() {
	excludePtr := flag.String("exclude", "", "directories to exclude")
	flag.Parse()

	fmt.Println("exclude:", *excludePtr)
	fmt.Println("tail:", flag.Args())

	roots := flag.Args()
	for i := range roots {
		filepath.Walk(roots[i], linecounter)
	}
}
