// Should count 11 lines
package main

import "fmt"

func main() {
    foo := `//  might
/*  consider
"   comments
*/  and
"   quotes
`
    fmt.Printf("%s\n", foo)
}
