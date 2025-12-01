package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const exrexMaxStrings = 50

// exrexStrings are the generated strings from the pattern.
// If [exrex] returns one string matching the original pattern, then this
// slice is nil, indicating no expansion was done.
type exrexStrings []string

// IsExpanded returns true if the pattern was expanded into multiple strings.
func (s exrexStrings) IsExpanded() bool {
	return s != nil
}

// exrex calls the exrex tool, which generates strings from regular expressions.
func exrex(re string) (exrexStrings, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "exrex", "-m", strconv.Itoa(exrexMaxStrings), re)
	slog.Debug(
		"running exrex command",
		"cmd", cmd.String())

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			slog.Warn(
				"exrex command failed",
				"status", exitErr.ExitCode(),
				"stderr", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("exrex failed: %w", err)
	}

	str := string(bytes.TrimSpace(out))
	if len(str) == 0 || str == re {
		return nil, nil
	}

	strs := exrexStrings(strings.Split(str, "\n"))
	if len(strs) == exrexMaxStrings {
		return nil, fmt.Errorf("exrex generated maximum number of strings (%d); pattern may be too broad", exrexMaxStrings)
	}

	return strs, nil
}
