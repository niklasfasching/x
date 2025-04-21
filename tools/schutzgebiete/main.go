package main

import (
	"log"
	"os"
)

func main() {
	switch os.Args[1] {
	case "verordnungen":
		if err := verordnungen("data/verordnungen"); err != nil {
			log.Fatal(err)
		}
	case "areas":
		if err := protectedAreas("data/areas"); err != nil {
			log.Fatal(err)
		}
	case "kml":
		if err := KML("data/verordnungen", "data/areas", "data/out.kml"); err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatal("unknownd cmd", os.Args[1])
	}
}
