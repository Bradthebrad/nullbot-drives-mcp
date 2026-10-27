package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/Bradthebrad/nullbot-drives-mcp/internal/drives"
	"github.com/Bradthebrad/tinychain/mcp"
)

const version = "0.1.0"

func main() {
	transport := flag.String("transport", "stdio", "Transport (stdio only for credential-bearing account tools).")
	showVersion := flag.Bool("version", false, "Print version and exit.")
	flag.Parse()
	if *showVersion {
		fmt.Println("nullbot-drives-mcp", version)
		return
	}
	if *transport != "stdio" {
		fmt.Fprintln(os.Stderr, "nullbot-drives-mcp: only stdio is supported; account tools must not be exposed over unauthenticated HTTP")
		os.Exit(2)
	}
	tools, err := drives.FromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nullbot-drives-mcp:", err)
		os.Exit(2)
	}
	server := mcp.NewServer("nullbot-drives-mcp")
	server.Version = version
	for _, tool := range tools.Tools() {
		server.AddTool(tool)
	}
	if err := server.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "nullbot-drives-mcp: transport failed")
		os.Exit(1)
	}
}
