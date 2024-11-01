time: src/go.mod src/go.sum src/main.go src/timeselectcamera/timeselectcamera.go src/timesyncsensor/timesyncsensor.go
	# the executable
	cd src && go build -o ../time -ldflags "-s -w" -tags osusergo,netgo
	file time

module.tar.gz: time
	# the bundled module
	rm -f $@
	tar czf $@ $^
