.PHONY: usage tests license

.DEFAULT: usage

export GO111MODULE=on

PACKAGE=citop
LICENSES_THIRD_PARTY=LICENSES_THIRD_PARTY

usage:
	@echo "Usage:"
	@echo "    make license         # Build third-party license"
	@echo "    make tests           # Run unit tests"

tests:
	go test -v ./...

license:
	{ \
	    echo -e "Below is a copy of the license of every package used by $(PACKAGE)" ; \
	    for pkg_dir in $$(go list -f '{{.Dir}}' -m all | grep -v $(PACKAGE)); \
	    do \
		find "$$pkg_dir" \
		    -maxdepth 1 \
		    -name LICENSE \
		    -exec echo -e "\n\n====== $$(echo $$pkg_dir | sed 's/.*\/\(go\/.*\)/\1/') ======\n" \; \
		    -exec fold -s {} \; ; \
	    done \
	} > $(LICENSES_THIRD_PARTY)

