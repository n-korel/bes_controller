module bucis-bes_simulator

go 1.23.0

toolchain go1.25.9

require (
	github.com/emiago/media v0.2.0
	github.com/emiago/sipgo v1.3.1
	github.com/emiago/sipgox v0.11.0
	github.com/pion/rtp v1.10.1
	github.com/rs/zerolog v1.35.1
	go.uber.org/goleak v1.3.0
)

replace github.com/emiago/sipgox => ./third_party/sipgox

replace github.com/emiago/sipgo => ./third_party/emiago-sipgo

replace github.com/emiago/media => ./third_party/emiago-media

require (
	github.com/gobwas/httphead v0.1.0 // indirect
	github.com/gobwas/pool v0.2.1 // indirect
	github.com/gobwas/ws v1.3.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/icholy/digest v1.1.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/rtcp v1.2.14 // indirect
	golang.org/x/sync v0.16.0 // indirect
	golang.org/x/sys v0.29.0 // indirect
)
