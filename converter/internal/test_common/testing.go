package test_common

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/grafana/agent/converter/diag"
	"github.com/stretchr/testify/require"
)

const (
	flowSuffix  = ".river"
	diagsSuffix = ".diags"
)

// TestDirectory will execute tests for converting from a source configuration
// file to a flow configuration file for all files in a provided folder path.
//
// For each file in the folderPath which ends with the sourceSuffix:
//
//  1. Execute the convert func on the content of each file.
//  2. Remove an Info diags from the results of calling convert in step 1.
//  3. If the current filename.sourceSuffix has a matching filename.diags, read
//     the contents of filename.diags and validate that they match in order
//     with the diags from step 2.
//  4. If the current filename.sourceSuffix has a matching filename.river, read
//     the contents of filename.river and validate that they match the river
//     configuration generated by calling convert in step 1.
func TestDirectory(t *testing.T, folderPath string, sourceSuffix string, convert func(in []byte) ([]byte, diag.Diagnostics)) {
	require.NoError(t, filepath.WalkDir(folderPath, func(path string, d fs.DirEntry, _ error) error {
		if d.IsDir() {
			return nil
		}

		if strings.HasSuffix(path, sourceSuffix) {
			tc := getTestCaseName(path, sourceSuffix)
			t.Run(tc, func(t *testing.T) {
				actualRiver, actualDiags := convert(getSourceContents(t, path))

				// Skip Info level diags for this testing. These would create
				// a lot of unnecessary noise.
				actualDiags.RemoveDiagsBySeverity(diag.SeverityLevelInfo)

				expectedDiags := getExpectedDiags(t, strings.TrimSuffix(path, sourceSuffix)+diagsSuffix)
				validateDiags(t, expectedDiags, actualDiags)

				expectedRiver := getExpectedRiver(t, path, sourceSuffix)
				validateRiver(t, expectedRiver, actualRiver)
			})
		}

		return nil
	}))
}

// getSourceContents reads the source file and retrieve its contents.
func getSourceContents(t *testing.T, path string) []byte {
	sourceBytes, err := os.ReadFile(path)
	require.NoError(t, err)
	return sourceBytes
}

// getTestCaseName gets the test case name based on the path and source suffix.
func getTestCaseName(path string, sourceSuffix string) string {
	caseName := filepath.Base(path)
	return strings.TrimSuffix(caseName, sourceSuffix)
}

// getExpectedDiags will retrieve any expected diags for the test.
func getExpectedDiags(t *testing.T, diagsFile string) []string {
	expectedDiags := []string{}
	if _, err := os.Stat(diagsFile); err == nil {
		errorBytes, err := os.ReadFile(diagsFile)
		require.NoError(t, err)
		errorsString := string(normalizeLineEndings(errorBytes))
		expectedDiags = strings.Split(errorsString, "\n")

		// Some error messages have \n in them and need this
		for ix := range expectedDiags {
			expectedDiags[ix] = strings.ReplaceAll(expectedDiags[ix], "\\n", "\n")
		}
	}

	return expectedDiags
}

// validateDiags makes sure the expected diags and actual diags are a match
func validateDiags(t *testing.T, expectedDiags []string, actualDiags diag.Diagnostics) {
	for ix, diag := range actualDiags {
		if len(expectedDiags) > ix {
			require.Equal(t, expectedDiags[ix], diag.String())
		} else {
			require.Fail(t, "unexpected diag count reach for diag: "+diag.String())
		}
	}

	// If we expect more diags than we got
	if len(expectedDiags) > len(actualDiags) {
		require.Fail(t, "missing expected diag: "+expectedDiags[len(actualDiags)])
	}
}

// normalizeLineEndings will replace '\r\n' with '\n'.
func normalizeLineEndings(data []byte) []byte {
	normalized := bytes.ReplaceAll(data, []byte{'\r', '\n'}, []byte{'\n'})
	return normalized
}

// getExpectedRiver reads the expected river output file and retrieve its contents.
func getExpectedRiver(t *testing.T, path string, sourceSuffix string) []byte {
	outputFile := strings.TrimSuffix(path, sourceSuffix) + flowSuffix
	if _, err := os.Stat(outputFile); err == nil {
		outputBytes, err := os.ReadFile(outputFile)
		require.NoError(t, err)
		return normalizeLineEndings(outputBytes)
	}

	return nil
}

// validateRiver makes sure the expected river and actual river are a match
func validateRiver(t *testing.T, expectedRiver []byte, actualRiver []byte) {
	if len(expectedRiver) > 0 {
		if !reflect.DeepEqual(expectedRiver, actualRiver) {
			fmt.Println("============== ACTUAL =============")
			fmt.Println(string(normalizeLineEndings(actualRiver)))
			fmt.Println("===================================")
		}

		require.Equal(t, string(expectedRiver), string(normalizeLineEndings(actualRiver)))
	}
}
