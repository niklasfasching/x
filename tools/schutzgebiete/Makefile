run: data/out.kml

data/verordnungen:
	go run *.go verordnungen

data/areas:
	go run *.go areas

data/out.kml: data/areas data/verordnungen
	go run *.go kml
