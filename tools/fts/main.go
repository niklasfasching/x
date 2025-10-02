// USAGE:
// $ go build -tags fts5,cshared -buildmode=c-shared -o fts.so x/tools/fts
// $ sqlite3 -cmd ".load ./fts" db.sqlite
package main

import "C"

import _ "github.com/niklasfasching/x/sq/fts"

func main() {}
