// Command samplegen writes every demo sample CSV to a directory (`make samples` -> samples/).
//
// Output is deterministic for a given date, and process dates are relative to that date (today by default),
// so files generated today are valid to upload today. bulkwire-service serves the same files fresh from
// GET /api/samples/{name}.csv, so this command is a convenience, not a requirement.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"bulkwire/internal/samples"
)

func main() {
	out := flag.String("out", "samples", "directory to write the CSV files to")
	date := flag.String("date", "", "generate as of this UTC date (YYYY-MM-DD); default today")
	flag.Parse()

	today := time.Now().UTC()
	if *date != "" {
		d, err := time.Parse(time.DateOnly, *date)
		if err != nil {
			fail("bad -date: %v", err)
		}
		today = d
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail("%v", err)
	}
	for _, info := range samples.Catalog() {
		data, err := samples.Generate(info.Name, today)
		if err != nil {
			fail("%v", err)
		}
		path := filepath.Join(*out, info.Name+".csv")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			fail("%v", err)
		}
		fmt.Printf("%-30s %6d rows  %s\n", info.Name, info.Rows, path)
	}
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "samplegen: "+format+"\n", a...)
	os.Exit(1)
}
