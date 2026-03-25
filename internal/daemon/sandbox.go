package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// generateSeatbeltProfile constructs a Seatbelt profile string for sandboxing bash commands.
// It expands glob patterns in allowWrite and denyRead to literal absolute paths.
func generateSeatbeltProfile(projectPath string, allowWrite, denyRead []string) (string, error) {
	var sb strings.Builder

	// Seatbelt profile header
	sb.WriteString("(version 1)\n")
	sb.WriteString("(deny default)\n")
	sb.WriteString("(allow file-read*)\n")
	sb.WriteString("(allow process*)\n")
	sb.WriteString("(allow sysctl*)\n")
	sb.WriteString("(allow signal)\n")
	sb.WriteString("(allow mach*)\n")
	sb.WriteString("(allow ipc*)\n")
	sb.WriteString("(deny file-write*)\n")

	// Process allowWrite patterns
	for _, pattern := range allowWrite {
		// Make pattern absolute if relative
		absPattern := pattern
		if !filepath.IsAbs(pattern) {
			absPattern = filepath.Join(projectPath, pattern)
		}

		// Try to expand as glob
		matches, err := doublestar.FilepathGlob(absPattern)
		if err != nil {
			return "", fmt.Errorf("failed to expand glob pattern %s: %w", pattern, err)
		}

		// If no matches, treat as literal path (check if it's a directory)
		if len(matches) == 0 {
			absPath, err := filepath.Abs(absPattern)
			if err != nil {
				return "", fmt.Errorf("failed to resolve absolute path for %s: %w", pattern, err)
			}
			// Check if path exists and is a directory
			if info, err := os.Stat(absPath); err == nil && info.IsDir() {
				sb.WriteString(fmt.Sprintf("(allow file-write* (subpath \"%s\"))\n", absPath))
			} else {
				// For non-existent or file paths, use literal
				sb.WriteString(fmt.Sprintf("(allow file-write* (literal \"%s\"))\n", absPath))
			}
		} else {
			// Emit one rule per matched file/directory
			for _, match := range matches {
				absPath, err := filepath.Abs(match)
				if err != nil {
					return "", fmt.Errorf("failed to resolve absolute path for %s: %w", match, err)
				}
				// Check if matched path is a directory
				if info, err := os.Stat(absPath); err == nil && info.IsDir() {
					sb.WriteString(fmt.Sprintf("(allow file-write* (subpath \"%s\"))\n", absPath))
				} else {
					sb.WriteString(fmt.Sprintf("(allow file-write* (literal \"%s\"))\n", absPath))
				}
			}
		}
	}

	// Process denyRead patterns
	for _, pattern := range denyRead {
		// Make pattern absolute if relative
		absPattern := pattern
		if !filepath.IsAbs(pattern) {
			absPattern = filepath.Join(projectPath, pattern)
		}

		// Expand tilde to home directory
		if strings.HasPrefix(absPattern, "~") {
			// Simple expansion - just for the tilde prefix
			homeDir := filepath.Join("/Users", strings.Split(absPattern, "/")[0][1:])
			if absPattern == "~" {
				absPattern = homeDir
			} else if strings.HasPrefix(absPattern, "~/") {
				absPattern = filepath.Join(homeDir, absPattern[2:])
			}
		}

		// Try to expand as glob
		matches, err := doublestar.FilepathGlob(absPattern)
		if err != nil {
			return "", fmt.Errorf("failed to expand glob pattern %s: %w", pattern, err)
		}

		// If no matches, treat as literal path
		if len(matches) == 0 {
			absPath, err := filepath.Abs(absPattern)
			if err != nil {
				return "", fmt.Errorf("failed to resolve absolute path for %s: %w", pattern, err)
			}
			sb.WriteString(fmt.Sprintf("(deny file-read* (literal \"%s\"))\n", absPath))
		} else {
			// Emit one rule per matched file
			for _, match := range matches {
				absPath, err := filepath.Abs(match)
				if err != nil {
					return "", fmt.Errorf("failed to resolve absolute path for %s: %w", match, err)
				}
				sb.WriteString(fmt.Sprintf("(deny file-read* (literal \"%s\"))\n", absPath))
			}
		}
	}

	return sb.String(), nil
}

// executeSandboxed runs a bash command within a macOS Seatbelt sandbox.
func executeSandboxed(ctx context.Context, command, cwd, projectPath string, allowWrite, denyRead []string) (string, error) {
	// Check if sandbox-exec exists
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		return "", fmt.Errorf("sandbox-exec not found (macOS only)")
	}

	// Check macOS version and warn about Seatbelt deprecation
	if versionOutput, err := exec.Command("sw_vers", "-productVersion").Output(); err == nil {
		versionStr := strings.TrimSpace(string(versionOutput))
		parts := strings.Split(versionStr, ".")
		if len(parts) > 0 {
			var majorVersion int
			fmt.Sscanf(parts[0], "%d", &majorVersion)
			if majorVersion >= 12 {
				LogWarn("Seatbelt is deprecated on macOS 12+; sandbox may not function correctly")
			}
		}
	}

	LogInfo("Executing sandboxed command: %s", command)

	// Generate Seatbelt profile
	profile, err := generateSeatbeltProfile(projectPath, allowWrite, denyRead)
	if err != nil {
		return "", fmt.Errorf("failed to generate sandbox profile: %w", err)
	}

	// Create temp file for profile
	tmpFile, err := os.CreateTemp("", "vix-sandbox-*.sb")
	if err != nil {
		return "", fmt.Errorf("failed to create temp profile file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	// Write profile to temp file
	if _, err := tmpFile.WriteString(profile); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("failed to write sandbox profile: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", fmt.Errorf("failed to close profile file: %w", err)
	}

	// Create sandboxed command with 120s timeout
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sandbox-exec", "-f", tmpPath, "bash", "-c", command)
	cmd.Dir = cwd

	output, err := cmd.CombinedOutput()
	result := string(output)

	// Truncate output if too large
	if len(result) > maxOutput {
		result = result[:maxOutput] + fmt.Sprintf("\n... (truncated at %d chars)", maxOutput)
	}

	// Handle errors
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "Error: command timed out after 120 seconds", nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			result += fmt.Sprintf("\n[exit code: %d]", exitErr.ExitCode())
		}
	}

	if result == "" {
		result = "(no output)"
	}

	return result, nil
}
