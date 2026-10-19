# Expected outputs

One file per example program that runs: `<name>.out` is what the program
prints, byte for byte, and `<name>.files/` holds the files it reads,
copied beside the binary before it runs. The suite test in
`examples/suite_test.go` builds every program under `../` with a file
here, as a release build and as a debug build, and compares. A program
without a file here is pending: the test names it and goes on, and the
stage's spec says what it waits for.
