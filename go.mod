module github.com/williamokano/insta-follower-tracker

// The declared language version, deliberately one release behind the toolchain
// everything is actually built with (1.27.1, set in the Dockerfile and the
// workflows). golangci-lint refuses to run against a module targeting a Go
// newer than the one its own release binary was built with, and it reads this
// line -- or the toolchain line, if one is present, which is why there is not
// one. Raising this to 1.27 breaks linting until golangci-lint ships a 1.27
// build, and buys nothing: no code here needs a 1.27 language feature, and the
// binary that ships is compiled by 1.27 either way.
go 1.26.0

require (
	golang.org/x/net v0.59.0
	modernc.org/sqlite v1.58.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.48.0 // indirect
	modernc.org/libc v1.75.7 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)
