// Command tool-catalog exports builtin contracts without accessing databases or providers.
package main

import (
	f "academic/agent/framework"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	check := flag.String("check", "", "Compare the registered catalog with this JSON file without modifying it")
	prefix := flag.String("prefix", "", "Export only tool names with this prefix")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(1)
	}
	r := f.NewToolRegistry()
	if err := (f.Builtins{}).Register(r); err != nil {
		panic(err)
	}
	items := []f.ToolSpec{}
	for _, s := range r.Catalog() {
		if strings.HasPrefix(s.Name, *prefix) {
			items = append(items, s)
		}
	}
	if *check != "" {
		stored, err := os.ReadFile(*check)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		var value any
		if json.Unmarshal(stored, &value) != nil {
			fmt.Fprintln(os.Stderr, "invalid catalog JSON")
			os.Exit(1)
		}
		left, _ := json.Marshal(value)
		rightRaw, _ := json.Marshal(items)
		var normalized any
		_ = json.Unmarshal(rightRaw, &normalized)
		right, _ := json.Marshal(normalized)
		if string(left) != string(right) {
			fmt.Fprintln(os.Stderr, "tool catalog differs from registered contracts; regenerate using this command")
			os.Exit(1)
		}
		fmt.Printf("Verified %d registered tool contracts against %s\n", len(items), *check)
		return
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(items); err != nil {
		panic(err)
	}
}
