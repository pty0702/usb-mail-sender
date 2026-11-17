// cmd/mailgrid/main.go
package main

import (
	"fmt"
	"os"

	"github.com/bravo1goingdark/mailgrid/cli"
	"github.com/bravo1goingdark/mailgrid/logger"
)

// Version information (set at build time)
var (
	version   = "dev"
	buildTime = "unknown"
	commit    = "unknown"
)

// main is the CLI entry point for mailgrid.
// It parses CLI flags and delegates execution to the CLI runner.
func main() {
	// Parse CLI flags into a structured config
	args := cli.ParseFlags()

	// Handle help flag (help shown by ParseFlags, just exit)
	if args.ShowHelp {
		return
	}

	// Handle version flag
	if args.ShowVersion {
		showVersion()
		return
	}

	// Configure structured logging (level + format) before any log output.
	logger.Init(args.LogLevel, args.LogFormat)

	// Flush CSV log files to disk when the process exits.
	defer logger.FlushAndClose()

	// Run the mailgrid workflow (load config, parse CSV, render/send emails)
	// 错误直接同步写 stderr：logrus 的 writer 是异步的，Fatalf 的 os.Exit
	// 会抢在内容刷出前退出，造成“退出码1但零输出”的静默失败。
	if err := cli.Run(args); err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		os.Exit(1)
	}
}

// showVersion displays version information
func showVersion() {
	fmt.Printf("MailGrid v%s\n", version)
	fmt.Printf("Built: %s\n", buildTime)
	fmt.Printf("Commit: %s\n", commit)
	fmt.Printf("\nMailGrid is a production-ready email orchestrator for bulk email campaigns.\n")
	fmt.Printf("Documentation: https://github.com/bravo1goingdark/mailgrid\n")
}
