TAGS := goexperiment.jsonv2,sqlite_omit_load_extension

watch:
	git ls-files | entr -r make run


watch-test:
	git ls-files | entr -r make test


run:
	TelegramAdminUsers='["dev"]' \
	AppSecret=$${AppSecret:-dev} \
		go run -tags $(TAGS) ./

build:
	go build -o bin -tags $(TAGS) ./

test:
	go test -p 1 -v -run Test$(NAME) -tags $(TAGS) ./...
