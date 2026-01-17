package review

import (
	"fmt"
	"strings"

	"github.com/yourusername/techy-bot/pkg/models"
)

// FormatReview formats Claude's review response for GitHub
func FormatReview(review string, mode models.ReviewMode) string {
	var sb strings.Builder

	// Header with mode indicator
	emoji := GetModeEmoji(mode)
	modeDesc := GetModeDescription(mode)
	sb.WriteString(fmt.Sprintf("## %s TechyBot %s\n\n", emoji, modeDesc))

	// Add the review content
	sb.WriteString(review)

	// Footer
	sb.WriteString("\n\n---\n")
	sb.WriteString("<sub>🤖 Powered by Claude | ")
	sb.WriteString("Triggered by `@techy ")
	sb.WriteString(string(mode))
	sb.WriteString("`</sub>")

	return sb.String()
}

// FormatInlineComment formats a comment for a specific line in the diff
func FormatInlineComment(body string, severity string) string {
	var prefix string
	switch severity {
	case "error":
		prefix = "🔴 **Error**: "
	case "warning":
		prefix = "🟠 **Warning**: "
	case "info":
		prefix = "🔵 **Info**: "
	default:
		prefix = ""
	}
	return prefix + body
}

// SplitIntoComments attempts to split a review into individual comments
// This is useful for posting inline comments on specific lines
func SplitIntoComments(review string) []models.ReviewComment {
	// This is a basic implementation - in practice, you might want
	// Claude to return structured output that can be parsed more reliably
	var comments []models.ReviewComment

	// Look for patterns like "In file.go:123" or "file.go line 123"
	// This is a simplified approach - a more robust solution would
	// use structured output from Claude

	lines := strings.Split(review, "\n")
	var currentComment strings.Builder
	var currentFile string
	var currentLine int

	for _, line := range lines {
		// Check if this line references a file location
		if file, lineNum, found := parseFileReference(line); found {
			// Save previous comment if any
			if currentFile != "" && currentComment.Len() > 0 {
				comments = append(comments, models.ReviewComment{
					Path: currentFile,
					Line: currentLine,
					Body: strings.TrimSpace(currentComment.String()),
				})
				currentComment.Reset()
			}
			currentFile = file
			currentLine = lineNum
		}

		if currentFile != "" {
			currentComment.WriteString(line)
			currentComment.WriteString("\n")
		}
	}

	// Don't forget the last comment
	if currentFile != "" && currentComment.Len() > 0 {
		comments = append(comments, models.ReviewComment{
			Path: currentFile,
			Line: currentLine,
			Body: strings.TrimSpace(currentComment.String()),
		})
	}

	return comments
}

// parseFileReference attempts to extract file and line number from text
func parseFileReference(text string) (file string, line int, found bool) {
	// Common patterns:
	// - "FILE: path/to/file.go:123"
	// - "In `file.go:123`"
	// - "file.go line 123"
	// - "file.go:123"

	// First check for FILE: prefix (preferred format)
	if strings.HasPrefix(strings.TrimSpace(text), "FILE:") {
		ref := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "FILE:"))
		if colonIdx := strings.LastIndex(ref, ":"); colonIdx != -1 {
			file = ref[:colonIdx]
			var lineNum int
			if _, err := fmt.Sscanf(ref[colonIdx+1:], "%d", &lineNum); err == nil {
				return file, lineNum, true
			}
		}
	}

	// Simple regex-free parsing for common patterns
	textLower := strings.ToLower(text)

	// Look for backtick-wrapped references
	if idx := strings.Index(textLower, "`"); idx != -1 {
		end := strings.Index(textLower[idx+1:], "`")
		if end != -1 {
			ref := text[idx+1 : idx+1+end]
			if colonIdx := strings.LastIndex(ref, ":"); colonIdx != -1 {
				file = ref[:colonIdx]
				var lineNum int
				if _, err := fmt.Sscanf(ref[colonIdx+1:], "%d", &lineNum); err == nil {
					return file, lineNum, true
				}
			}
		}
	}

	return "", 0, false
}

// ParseStructuredReview parses Claude's output for FILE: and COMMENT: markers
func ParseStructuredReview(review string) (summary string, comments []models.ReviewComment) {
	lines := strings.Split(review, "\n")
	var summaryBuilder strings.Builder
	var currentFile string
	var currentLine int
	var currentComment strings.Builder
	inComment := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check for FILE: marker
		if strings.HasPrefix(trimmed, "FILE:") {
			// Save previous comment if any
			if currentFile != "" && currentComment.Len() > 0 {
				comments = append(comments, models.ReviewComment{
					Path: currentFile,
					Line: currentLine,
					Body: strings.TrimSpace(currentComment.String()),
				})
				currentComment.Reset()
			}

			// Parse new file reference
			if file, lineNum, found := parseFileReference(trimmed); found {
				currentFile = file
				currentLine = lineNum
				inComment = false
			}
			continue
		}

		// Check for COMMENT: marker
		if strings.HasPrefix(trimmed, "COMMENT:") {
			inComment = true
			commentText := strings.TrimSpace(strings.TrimPrefix(trimmed, "COMMENT:"))
			if commentText != "" {
				currentComment.WriteString(commentText)
				currentComment.WriteString("\n")
			}
			continue
		}

		// Accumulate comment or summary
		if inComment && currentFile != "" {
			currentComment.WriteString(line)
			currentComment.WriteString("\n")
		} else if !inComment {
			summaryBuilder.WriteString(line)
			summaryBuilder.WriteString("\n")
		}
	}

	// Don't forget the last comment
	if currentFile != "" && currentComment.Len() > 0 {
		comments = append(comments, models.ReviewComment{
			Path: currentFile,
			Line: currentLine,
			Body: strings.TrimSpace(currentComment.String()),
		})
	}

	summary = strings.TrimSpace(summaryBuilder.String())
	return
}

// TruncateForGitHub truncates content to fit GitHub's comment size limit
func TruncateForGitHub(content string, maxLength int) string {
	if maxLength <= 0 {
		maxLength = 65536 // GitHub's limit
	}

	if len(content) <= maxLength {
		return content
	}

	// Find a good break point (end of a line)
	truncateAt := maxLength - 100 // Leave room for the truncation notice
	for i := truncateAt; i > truncateAt-500 && i > 0; i-- {
		if content[i] == '\n' {
			truncateAt = i
			break
		}
	}

	return content[:truncateAt] + "\n\n---\n⚠️ *Review truncated due to length. Some findings may not be shown.*"
}

// CollapsibleSection wraps content in a collapsible details section
func CollapsibleSection(summary, content string) string {
	return fmt.Sprintf("<details>\n<summary>%s</summary>\n\n%s\n\n</details>", summary, content)
}

// FormatFileSummary creates a summary of files changed
func FormatFileSummary(files []models.PRFile) string {
	if len(files) == 0 {
		return "No files changed"
	}

	var sb strings.Builder
	sb.WriteString("| File | Status | Changes |\n")
	sb.WriteString("|------|--------|--------|\n")

	for _, f := range files {
		status := f.Status
		if status == "" {
			status = "modified"
		}
		sb.WriteString(fmt.Sprintf("| `%s` | %s | +%d/-%d |\n",
			f.Filename, status, f.Additions, f.Deletions))
	}

	return sb.String()
}
