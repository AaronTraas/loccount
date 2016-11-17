# Makefile for loccount
# You must have the Go compiler and tools installed to build this software.

loccount:
	go build

clean:
	go clean
	rm -f loccount.html loccount.1

install:
	go install

check: loccount 
	@./loccount -i tests | diff -u check.good -
	@echo "No output is good news"

testbuild: loccount
	./loccount -i tests >check.good

.SUFFIXES: .html .txt .1

# Requires asciidoc and xsltproc/docbook stylesheets.
.txt.1:
	a2x --doctype manpage --format manpage $<
.txt.html:
	a2x --doctype manpage --format xhtml -D . $<
	rm -f docbook-xsl.css
