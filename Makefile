# Makefile for loccount
# You must have the Go compiler and tools installed to build this software.

VERS=$(shell sed <loccount -n -e '/version string *= *\"\(.*\)\"/s//\1/p')

loccount: loccount.go
	go build

clean:
	go clean
	rm -f *.html *.1

install: loccount
	go install

check: loccount 
	@(./loccount -i tests; ./loccount -u tests) | diff -u check.good -
	@echo "No output is good news"

testbuild: loccount
	@(./loccount -i tests; ./loccount -u tests) >check.good

SOURCES = README COPYING NEWS control loccount.go loccount.adoc \
		Makefile TODO loccount-logo.png check.good tests/

.SUFFIXES: .html .adoc .1

# Requires asciidoc and xsltproc/docbook stylesheets.
.adoc.1:
	a2x --doctype manpage --format manpage $<
.adoc.html:
	a2x --doctype manpage --format xhtml -D . $<
	rm -f docbook-xsl.css

VERS=$(shell sed <loccount.go -n -e '/.*version.*= *\(.*\)/s//\1/p')

version:
	@echo $(VERS)

# Report which languages lack lloc support
sloc: loccount
	loccount -i -s >/tmp/sloc$$; loccount -i -l >/tmp/lloc$$
	comm -23 /tmp/sloc$$ /tmp/lloc$$
	rm -f /tmp/sloc$$ /tmp/lloc$$

loccount-$(VERS).tar.gz: $(SOURCES) loccount.1
	tar --transform='s:^:loccount-$(VERS)/:' --show-transformed-names -cvzf loccount-$(VERS).tar.gz $(SOURCES) loccount.1

dist: loccount-$(VERS).tar.gz

release: loccount-$(VERS).tar.gz loccount.html
	shipper version=$(VERS) | sh -e -x

refresh: loccount.html
	shipper -N -w version=$(VERS) | sh -e -x
