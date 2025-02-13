package earthfile2llb

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildArgMatrix(t *testing.T) {
	var tests = []struct {
		in  []string
		out [][]string
	}{
		{[]string{}, [][]string{nil}},
		{[]string{"a=1"}, [][]string{{"a=1"}}},
		{[]string{"a=1", "a=2", "a=3"}, [][]string{{"a=1"}, {"a=2"}, {"a=3"}}},
		{[]string{"a=1", "b=2"}, [][]string{{"a=1", "b=2"}}},
		{[]string{"a=1", "a=3", "b=2"}, [][]string{{"a=1", "b=2"}, {"a=3", "b=2"}}},
		{[]string{"a=1", "a=3", "b=2", "b=4"}, [][]string{{"a=1", "b=2"}, {"a=1", "b=4"}, {"a=3", "b=2"}, {"a=3", "b=4"}}},
		{[]string{"a=1", "b=2", "a=3", "b=4"}, [][]string{{"a=1", "b=2"}, {"a=1", "b=4"}, {"a=3", "b=2"}, {"a=3", "b=4"}}},
		{[]string{"a=1", "b=2", "a=3", "b=4", "c=10"}, [][]string{{"a=1", "b=2", "c=10"}, {"a=1", "b=4", "c=10"}, {"a=3", "b=2", "c=10"}, {"a=3", "b=4", "c=10"}}},
		{[]string{"a=1", "a=3", "a=7", "c=10"}, [][]string{{"a=1", "c=10"}, {"a=3", "c=10"}, {"a=7", "c=10"}}},
	}

	for _, tt := range tests {
		ans, err := buildArgMatrix(tt.in)
		assert.NoError(t, err)
		assert.Equal(t, tt.out, ans)
	}
}

func TestParseParans(t *testing.T) {
	var tests = []struct {
		in    string
		first string
		args  []string
	}{
		{"(+target/art --flag=something)", "+target/art", []string{"--flag=something"}},
		{"(+target/art --flag=something\"\")", "+target/art", []string{"--flag=something\"\""}},
		{"( \n  +target/art \t \n --flag=something\t   )", "+target/art", []string{"--flag=something"}},
		{"(+target/art --flag=something\\ --another=something)", "+target/art", []string{"--flag=something\\ --another=something"}},
		{"(+target/art --flag=something --another=something)", "+target/art", []string{"--flag=something", "--another=something"}},
		{"(+target/art --flag=\"something in quotes\")", "+target/art", []string{"--flag=\"something in quotes\""}},
		{"(+target/art --flag=\\\"something --not=in-quotes\\\")", "+target/art", []string{"--flag=\\\"something", "--not=in-quotes\\\""}},
		{"(+target/art --flag=look-ma-a-\\))", "+target/art", []string{"--flag=look-ma-a-\\)"}},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			actualFirst, actualArgs, err := parseParans(tt.in)
			assert.NoError(t, err)
			assert.Equal(t, tt.first, actualFirst)
			assert.Equal(t, tt.args, actualArgs)
		})

	}
}

func TestNegativeParseParans(t *testing.T) {
	var tests = []struct {
		in string
	}{
		{"+target/art --flag=something)"},
		{"(+target/art --flag=something"},
		{"(+target/art --flag=\"something)"},
		{"(+target/art --flag=something\\)"},
		{"()"},
		{"(          \t\n   )"},
	}

	for _, tt := range tests {
		_, _, err := parseParans(tt.in)
		assert.Error(t, err)
	}
}

func TestProcessParansAndQuotes(t *testing.T) {
	var tests = []struct {
		in   []string
		args []string
	}{
		{[]string{}, []string{}},
		{[]string{""}, []string{""}},
		{[]string{"abc", "def", "ghi"}, []string{"abc", "def", "ghi"}},
		{[]string{"hello ", "wor(", "ld)"}, []string{"hello ", "wor( ld)"}},
		{[]string{"hello ", "(wor(", "ld)"}, []string{"hello ", "(wor( ld)"}},
		{[]string{"hello ", "\"(wor(\"", "ld)"}, []string{"hello ", "\"(wor(\"", "ld)"}},
		{[]string{"let's", "go"}, []string{"let's go"}},
		{[]string{"(hello)"}, []string{"(hello)"}},
		{[]string{"  (hello)"}, []string{"  (hello)"}},
		{[]string{"(hello", "    ooo)"}, []string{"(hello     ooo)"}},
		{[]string{"--load=(+a-test-image", "--name=foo", "--var", "bar)"}, []string{"--load=(+a-test-image --name=foo --var bar)"}},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.in, " "), func(t *testing.T) {
			actualArgs := processParansAndQuotes(tt.in)
			assert.Equal(t, tt.args, actualArgs)
		})

	}
}
