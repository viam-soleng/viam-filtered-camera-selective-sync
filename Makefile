time: *.go */*.go go.*
	# the executable
	go build -o $@ -ldflags "-s -w" -tags osusergo,netgo
	file $@

module.tar.gz: time meta.json
	# the bundled module
	rm -f $@
	tar czf $@ time meta.json
