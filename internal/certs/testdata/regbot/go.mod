module io.github/murmel/internal/certs/testdata/regbot

// TEST-ONLY throwaway module for the T7 murmur registration scenario.
// Nested in testdata so the parent module ignores it entirely.

go 1.26

require layeh.com/gumble v0.0.0-20221205141517-d1df60a3cc14

require (
	github.com/golang/protobuf v1.3.1 // indirect
	layeh.com/gopus v0.0.0-20161224163843-0ebf989153aa // indirect
)

replace io.github/murmel => ../../../..

require io.github/murmel v0.0.0
