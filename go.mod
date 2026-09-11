module mywalkapp

go 1.22.2

require github.com/lxn/walk v0.0.0-20210112085537-c389da54e794

require (
	github.com/lxn/win v0.0.0-20210218163916-a377121e959e // indirect
	golang.org/x/sys v0.47.0 // indirect
	gopkg.in/Knetic/govaluate.v3 v3.0.0 // indirect
)

// NOTE: these two replace directives only exist because the sandbox this
// was built in could reach github.com but not golang.org or gopkg.in.
// On a normal machine with full internet access they're unnecessary (but
// harmless) -- delete them and run `go mod tidy` if you'd rather pull
// from the canonical hosts.
replace golang.org/x/sys => github.com/golang/sys v0.28.0

replace gopkg.in/Knetic/govaluate.v3 => github.com/Knetic/govaluate v3.0.0+incompatible
