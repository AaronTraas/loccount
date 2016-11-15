# Makefile for loccount
# You must have the Go compiler installed to build this software
# Conventional install and uninstall productions are omitted; use "go install"

loccount:
	go build

check: loccount 
	@./loccount tests | diff -u check.good -
	@echo "No output is good news"

testbuild: loccount
	./loccount tests >check.good

