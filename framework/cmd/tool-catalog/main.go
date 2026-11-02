// Command tool-catalog exports builtin contracts without accessing databases or providers.
package main

import (
	f "academic/agent/framework"
	"encoding/json"
	"os"
)

func main() {
	r := f.NewToolRegistry()
	if err := (f.Builtins{}).Register(r); err != nil {
		panic(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(r.Catalog()); err != nil {
		panic(err)
	}
}
