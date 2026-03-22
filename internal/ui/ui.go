package ui

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

var stdinScanner *bufio.Scanner

func init() {
	stdinScanner = bufio.NewScanner(os.Stdin)
}

func noColor() bool {
	_, ok := os.LookupEnv("NO_COLOR")
	return ok
}

// IsInteractive returns true if stdin is a terminal.
func IsInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func ansi(code string, s string) string {
	if noColor() {
		return s
	}
	return code + s + "\033[0m"
}

func Bold(s string) string   { return ansi("\033[1m", s) }
func Green(s string) string  { return ansi("\033[32m", s) }
func Red(s string) string    { return ansi("\033[31m", s) }
func Yellow(s string) string { return ansi("\033[33m", s) }
func Cyan(s string) string   { return ansi("\033[36m", s) }
func Gray(s string) string   { return ansi("\033[90m", s) }

func Success(msg string) { fmt.Println(Green("✓") + " " + msg) }
func Error(msg string)   { fmt.Fprintln(os.Stderr, Red("✗")+" "+msg) }
func Warn(msg string)    { fmt.Println(Yellow("!") + " " + msg) }
func Info(msg string)    { fmt.Println(Cyan("→") + " " + msg) }

// Step prints a verbose step message (only when verbose is true).
func Step(verbose bool, msg string) {
	if verbose {
		fmt.Println(Gray("  · " + msg))
	}
}

func Table(headers []string, rows [][]string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	headerLine := strings.Join(headers, "\t")
	fmt.Fprintln(w, Bold(headerLine))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	w.Flush()
}

func readLine() (string, bool) {
	if stdinScanner.Scan() {
		return strings.TrimSpace(stdinScanner.Text()), true
	}
	return "", false
}

func Prompt(label string) string {
	fmt.Print(Bold(label + ": "))
	line, _ := readLine()
	return line
}

func Confirm(label string) bool {
	fmt.Print(Bold(label + " [y/N]: "))
	line, ok := readLine()
	if !ok {
		return false
	}
	ans := strings.ToLower(line)
	return ans == "y" || ans == "yes"
}

func Select(label string, options []string) (int, error) {
	fmt.Println(Bold(label + ":"))
	for i, opt := range options {
		fmt.Printf("  %s %s\n", Cyan(fmt.Sprintf("[%d]", i+1)), opt)
	}
	for {
		fmt.Print(Bold("Select: "))
		line, ok := readLine()
		if !ok {
			return -1, fmt.Errorf("no input available (stdin closed)")
		}
		n, err := strconv.Atoi(line)
		if err == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		fmt.Println(Red("Invalid selection, try again."))
	}
}

func MultiSelect(label string, options []string) ([]int, error) {
	fmt.Println(Bold(label + " (enter comma-separated numbers, or empty to skip):"))
	for i, opt := range options {
		fmt.Printf("  %s %s\n", Cyan(fmt.Sprintf("[%d]", i+1)), opt)
	}
	for {
		fmt.Print(Bold("Select: "))
		line, ok := readLine()
		if !ok {
			return nil, fmt.Errorf("no input available (stdin closed)")
		}
		if line == "" {
			return nil, nil
		}
		parts := strings.Split(line, ",")
		var selected []int
		valid := true
		for _, p := range parts {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || n < 1 || n > len(options) {
				valid = false
				break
			}
			selected = append(selected, n-1)
		}
		if valid {
			return selected, nil
		}
		fmt.Println(Red("Invalid selection, try again."))
	}
}

func Spinner(label string) func() {
	// Non-interactive: print once, no animation
	if !IsInteractive() {
		fmt.Printf("%s...\n", label)
		return func() {}
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0
		for {
			select {
			case <-stop:
				fmt.Print("\r\033[K")
				return
			default:
				if noColor() {
					fmt.Printf("\r%s %s", label, "...")
				} else {
					fmt.Printf("\r%s %s", Cyan(frames[i%len(frames)]), label)
				}
				i++
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
	return func() {
		close(stop)
		wg.Wait()
	}
}
