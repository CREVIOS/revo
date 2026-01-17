package github

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/CREVIOS/revo/pkg/models"
	"github.com/google/go-github/v60/github"
)

// ParseDiff parses a unified diff string into structured file changes
func ParseDiff(diff string) []models.PRFile {
	var files []models.PRFile

	// Split diff by file
	filePattern := regexp.MustCompile(`(?m)^diff --git a/(.+?) b/(.+?)$`)
	matches := filePattern.FindAllStringSubmatchIndex(diff, -1)

	for i, match := range matches {
		start := match[0]
		end := len(diff)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}

		fileDiff := diff[start:end]
		file := parseFileDiff(fileDiff)
		if file != nil {
			files = append(files, *file)
		}
	}

	return files
}

// parseFileDiff parses a single file's diff section
func parseFileDiff(fileDiff string) *models.PRFile {
	lines := strings.Split(fileDiff, "\n")
	if len(lines) == 0 {
		return nil
	}

	file := &models.PRFile{}

	// Parse header line: diff --git a/path b/path
	headerPattern := regexp.MustCompile(`^diff --git a/(.+?) b/(.+?)$`)
	if match := headerPattern.FindStringSubmatch(lines[0]); match != nil {
		file.PreviousName = match[1]
		file.Filename = match[2]
	}

	// Determine file status and count changes
	for _, line := range lines {
		if strings.HasPrefix(line, "new file") {
			file.Status = "added"
		} else if strings.HasPrefix(line, "deleted file") {
			file.Status = "removed"
		} else if strings.HasPrefix(line, "rename from") {
			file.Status = "renamed"
		} else if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			file.Additions++
			file.Changes++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			file.Deletions++
			file.Changes++
		}
	}

	// Default status to modified if not set
	if file.Status == "" {
		file.Status = "modified"
	}

	// Store the patch (everything after the header)
	patchStart := 0
	for i, line := range lines {
		if strings.HasPrefix(line, "@@") {
			patchStart = i
			break
		}
	}
	if patchStart > 0 {
		file.Patch = strings.Join(lines[patchStart:], "\n")
	}

	return file
}

// ConvertGitHubFiles converts GitHub API file objects to our model
func ConvertGitHubFiles(ghFiles []*github.CommitFile) []models.PRFile {
	files := make([]models.PRFile, 0, len(ghFiles))

	for _, f := range ghFiles {
		file := models.PRFile{
			Filename:  f.GetFilename(),
			Status:    f.GetStatus(),
			Additions: f.GetAdditions(),
			Deletions: f.GetDeletions(),
			Changes:   f.GetChanges(),
			Patch:     f.GetPatch(),
		}

		if f.GetPreviousFilename() != "" {
			file.PreviousName = f.GetPreviousFilename()
		}

		files = append(files, file)
	}

	return files
}

// HunkInfo contains information about a diff hunk
type HunkInfo struct {
	OldStart int
	OldLines int
	NewStart int
	NewLines int
}

// ParseHunkHeader parses a diff hunk header line
// Format: @@ -oldStart,oldLines +newStart,newLines @@ optional context
func ParseHunkHeader(header string) *HunkInfo {
	pattern := regexp.MustCompile(`@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
	match := pattern.FindStringSubmatch(header)
	if match == nil {
		return nil
	}

	hunk := &HunkInfo{}

	hunk.OldStart, _ = strconv.Atoi(match[1])
	if match[2] != "" {
		hunk.OldLines, _ = strconv.Atoi(match[2])
	} else {
		hunk.OldLines = 1
	}

	hunk.NewStart, _ = strconv.Atoi(match[3])
	if match[4] != "" {
		hunk.NewLines, _ = strconv.Atoi(match[4])
	} else {
		hunk.NewLines = 1
	}

	return hunk
}

// TruncateDiff truncates a diff to maxSize bytes, preserving file boundaries
func TruncateDiff(diff string, maxSize int) string {
	if len(diff) <= maxSize {
		return diff
	}

	// Find the last complete file diff within the size limit
	filePattern := regexp.MustCompile(`(?m)^diff --git`)
	matches := filePattern.FindAllStringIndex(diff, -1)

	lastValidEnd := 0
	for _, match := range matches {
		if match[0] <= maxSize {
			lastValidEnd = match[0]
		} else {
			break
		}
	}

	if lastValidEnd > 0 {
		return diff[:lastValidEnd] + "\n\n[Diff truncated due to size limits]"
	}

	// Fallback: hard truncate
	return diff[:maxSize] + "\n\n[Diff truncated due to size limits]"
}

// GetChangedLineNumbers extracts the line numbers that were changed in a patch
func GetChangedLineNumbers(patch string) map[int]bool {
	changed := make(map[int]bool)
	lines := strings.Split(patch, "\n")

	var currentLine int
	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			hunk := ParseHunkHeader(line)
			if hunk != nil {
				currentLine = hunk.NewStart
			}
			continue
		}

		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			changed[currentLine] = true
			currentLine++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			// Deleted lines don't increment the current line number
			continue
		} else {
			currentLine++
		}
	}

	return changed
}
