package promote

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// imageTagPath is the one key a promote is about: the service's own
// container image tag at the root of its Helm values file.
//
// Everything else in a values file that happens to be spelled "tag:" —
// a sidecar, an init container, a sub-chart, an agent image — belongs to
// some *other* repository, for which this service's image SHA does not
// exist. Rewriting those is how a green promote puts production into
// ImagePullBackOff, so the carry addresses this path and nothing else.
var imageTagPath = []string{"image", "tag"}

// tagPattern is the shape of tag this package is willing to write into a
// values file. It is deliberately narrow: no quotes, no backslash, no
// "#", no whitespace, no YAML flow punctuation, and an alphanumeric
// first character. That is what keeps the edit below safe to splice in
// literally, whatever quoting style the file already uses.
var tagPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+:@/-]{0,127}$`)

// maxSiblings caps the values-directory listing put in a warning/audit
// row — enough to spot a naming mismatch, not a directory dump.
const maxSiblings = 8

// valuesRel is the repo-relative values path for env ("dev" or "prod").
func valuesRel(env, service string) string {
	return filepath.Join("gitops", "values", env, service+".yaml")
}

// ReadProdTag returns image.tag from gitops/values/prod/<service>.yaml
// in repoDir. Empty string + nil if the file is missing (no gitops/).
func ReadProdTag(repoDir, service string) (string, error) {
	return readTag(filepath.Join(repoDir, valuesRel("prod", service)))
}

// ReadDevTag returns image.tag from gitops/values/dev/<service>.yaml.
func ReadDevTag(repoDir, service string) (string, error) {
	return readTag(filepath.Join(repoDir, valuesRel("dev", service)))
}

func readTag(path string) (string, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path under agent-owned repo clone
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("promote: read values: %w", err)
	}
	node, err := findImageTag(raw)
	if err != nil {
		return "", fmt.Errorf("promote: %s: %w", path, err)
	}
	return node.Value, nil
}

// CarryStatus is what a tag carry actually did.
//
// It exists because "changed or not" was too little information: a
// promote that carried nothing looked exactly like one that carried a
// tag, in the log and in the audit row, while production quietly kept
// its old image.
type CarryStatus string

const (
	// CarryUpdated means the prod values file was rewritten with the dev tag.
	CarryUpdated CarryStatus = "updated"
	// CarryCurrent means prod already held the dev tag; nothing to commit.
	CarryCurrent CarryStatus = "already_current"
	// CarryNoValues means this service has no values file on either
	// side, so the promote is a branch fast-forward only. Supported,
	// but never silent — see CarryProdTag.
	CarryNoValues CarryStatus = "no_values_file"
)

// Carry is the outcome of CarryProdTag.
type Carry struct {
	Status CarryStatus
	// Tag is the dev image.tag; empty when there was nothing to read.
	Tag string
	// PrevTag is prod's image.tag before the carry.
	PrevTag string
	// DevPath / ProdPath are the repo-relative values paths that were
	// looked for, present or not.
	DevPath  string
	ProdPath string
	// Siblings are the file names actually present in
	// gitops/values/prod when this service's own file was not. This is
	// the diagnostic for a values file named after something other than
	// repos[].name.
	Siblings []string
}

// Changed reports whether the prod values file on disk was rewritten and
// therefore needs committing.
func (c Carry) Changed() bool { return c.Status == CarryUpdated }

// CarryProdTag copies image.tag from gitops/values/dev/<service>.yaml
// into gitops/values/prod/<service>.yaml in repoDir, touching only that
// one key.
//
// Three outcomes, all reported rather than swallowed:
//
//   - Both files present: the tag is carried (CarryUpdated) or was
//     already there (CarryCurrent).
//   - Neither file present: CarryNoValues, no error. A repo that
//     promotes by fast-forward alone is a supported topology, so this
//     must not fail — but the caller is expected to log it and put it
//     in the audit row, because it is also what a values file named
//     after something other than repos[].name looks like from here.
//     Carry.Siblings then names what the values directory does contain.
//   - Exactly one file present: an error. That is half a carry
//     configuration — the repo does pin its image in git, but the tag
//     has nowhere to go (or nowhere to come from). Fast-forwarding
//     would advertise a promote that did not happen, so refuse.
func CarryProdTag(repoDir, service string) (Carry, error) {
	devRel := valuesRel("dev", service)
	prodRel := valuesRel("prod", service)
	out := Carry{DevPath: devRel, ProdPath: prodRel}
	devPath := filepath.Join(repoDir, devRel)
	prodPath := filepath.Join(repoDir, prodRel)

	devRaw, devErr := os.ReadFile(devPath)    //nolint:gosec // path under agent-owned repo clone
	prodRaw, prodErr := os.ReadFile(prodPath) //nolint:gosec // path under agent-owned repo clone
	devMissing, prodMissing := os.IsNotExist(devErr), os.IsNotExist(prodErr)
	switch {
	case devMissing && prodMissing:
		out.Status = CarryNoValues
		out.Siblings = siblingValues(filepath.Dir(prodPath), filepath.Dir(devPath))
		return out, nil
	case devMissing || prodMissing:
		have, missing := devRel, prodRel
		if devMissing {
			have, missing = prodRel, devRel
		}
		return out, fmt.Errorf(
			"promote: %s exists but %s does not: create it, or remove %s if this service promotes by fast-forward alone",
			have, missing, have)
	}
	if devErr != nil {
		return out, fmt.Errorf("promote: read dev values: %w", devErr)
	}
	if prodErr != nil {
		return out, fmt.Errorf("promote: read prod values: %w", prodErr)
	}

	devNode, err := findImageTag(devRaw)
	if err != nil {
		return out, fmt.Errorf("promote: %s: %w", devRel, err)
	}
	prodNode, err := findImageTag(prodRaw)
	if err != nil {
		return out, fmt.Errorf("promote: %s: %w", prodRel, err)
	}
	out.Tag, out.PrevTag = devNode.Value, prodNode.Value
	if out.PrevTag == out.Tag {
		out.Status = CarryCurrent
		return out, nil
	}

	newProd, err := setImageTag(prodRaw, out.Tag)
	if err != nil {
		return out, fmt.Errorf("promote: %s: %w", prodRel, err)
	}
	// GitOps values are world-readable by design (committed YAML).
	if err := os.WriteFile(prodPath, newProd, 0o644); err != nil { //nolint:gosec // G306: values.yaml must stay 0644 for git
		return out, fmt.Errorf("promote: write prod values: %w", err)
	}
	out.Status = CarryUpdated
	return out, nil
}

// siblingValues lists the *.yaml file names in the first of dirs that
// exists. Bounded by maxSiblings; nil when no values directory is there
// at all (a repo with no gitops/ tree, which is the honest no-op case).
func siblingValues(dirs ...string) []string {
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if n := e.Name(); strings.HasSuffix(n, ".yaml") || strings.HasSuffix(n, ".yml") {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			continue
		}
		sort.Strings(names)
		if len(names) > maxSiblings {
			names = append(names[:maxSiblings:maxSiblings], "...")
		}
		return names
	}
	return nil
}

// findImageTag parses raw as YAML and returns the scalar node at
// imageTagPath. The node carries Line/Column, which is what lets the
// edit below be structural *and* byte-preserving.
func findImageTag(raw []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse values: %w", err)
	}
	path := strings.Join(imageTagPath, ".")
	node := lookup(&doc, imageTagPath...)
	if node == nil {
		return nil, fmt.Errorf("no %s key", path)
	}
	if node.Kind != yaml.ScalarNode {
		return nil, fmt.Errorf("%s is not a scalar", path)
	}
	return node, nil
}

// lookup walks a mapping path from a document node. Later duplicate keys
// win, matching how a YAML loader would resolve the file.
func lookup(n *yaml.Node, path ...string) *yaml.Node {
	cur := n
	if cur.Kind == yaml.DocumentNode {
		if len(cur.Content) == 0 {
			return nil
		}
		cur = cur.Content[0]
	}
	for _, key := range path {
		if cur == nil || cur.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(cur.Content); i += 2 {
			if cur.Content[i].Value == key {
				next = cur.Content[i+1]
			}
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

// setImageTag returns raw with only the image.tag scalar replaced by
// tag.
//
// Why this and not a yaml.Node round trip: re-marshalling a parsed
// document rewrites the whole file — indentation is normalised, blank
// lines are dropped, quoting can change. A GitOps repo with a YAML
// formatter or an end-of-file-fixer hook would then fight the promote
// commit. So the parse is used only to *locate* the key (which is what
// a regexp cannot do reliably), and the write is a splice of the value
// token at that exact position. Every other byte of the file — layout,
// comments, blank lines, the trailing newline — is carried through
// untouched.
func setImageTag(raw []byte, tag string) ([]byte, error) {
	if !tagPattern.MatchString(tag) {
		return nil, fmt.Errorf("refusing to write image tag %q: not a plain image tag", tag)
	}
	node, err := findImageTag(raw)
	if err != nil {
		return nil, err
	}
	if node.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return nil, errors.New("image.tag is a block scalar; refusing to edit it in place")
	}
	start, err := offsetOf(raw, node.Line, node.Column)
	if err != nil {
		return nil, err
	}
	lo, hi, err := scalarSpan(raw, start)
	if err != nil {
		return nil, err
	}
	if lo == hi {
		return nil, errors.New("image.tag has no value to replace")
	}

	out := make([]byte, 0, len(raw)-(hi-lo)+len(tag))
	out = append(out, raw[:lo]...)
	out = append(out, tag...)
	out = append(out, raw[hi:]...)

	// Cheap proof the splice landed on the right token: the file still
	// parses and image.tag is now exactly the tag. A value the scan
	// mis-measured (a flow mapping, an exotic scalar) fails here instead
	// of being committed.
	check, err := findImageTag(out)
	if err != nil {
		return nil, fmt.Errorf("in-place edit produced invalid YAML: %w", err)
	}
	if check.Value != tag {
		return nil, fmt.Errorf("in-place edit did not take: image.tag = %q, want %q", check.Value, tag)
	}
	return out, nil
}

// offsetOf converts a yaml.Node 1-based line/column into a byte offset.
// Columns are counted in runes, as the YAML scanner counts them.
func offsetOf(raw []byte, line, col int) (int, error) {
	if line < 1 || col < 1 {
		return 0, fmt.Errorf("image.tag has no source position (%d:%d)", line, col)
	}
	off := 0
	for l := 1; l < line; l++ {
		i := bytes.IndexByte(raw[off:], '\n')
		if i < 0 {
			return 0, fmt.Errorf("image.tag line %d is past end of file", line)
		}
		off += i + 1
	}
	for c := 1; c < col; c++ {
		if off >= len(raw) || raw[off] == '\n' {
			return 0, fmt.Errorf("image.tag column %d is past end of line %d", col, line)
		}
		_, size := utf8.DecodeRune(raw[off:])
		off += size
	}
	if off >= len(raw) || raw[off] == '\n' {
		return 0, fmt.Errorf("image.tag at %d:%d has no value on that line", line, col)
	}
	return off, nil
}

// scalarSpan returns the byte span of the scalar's *text* starting at
// start — inside the quotes when it is quoted, so the file keeps the
// quoting style it already had, and stopping before a trailing comment
// or trailing blanks when it is plain.
func scalarSpan(raw []byte, start int) (int, int, error) {
	switch raw[start] {
	case '"':
		for i := start + 1; i < len(raw); i++ {
			switch raw[i] {
			case '\\':
				i++
			case '"':
				return start + 1, i, nil
			case '\n':
				return 0, 0, errors.New("image.tag spans more than one line; refusing to edit it in place")
			}
		}
		return 0, 0, errors.New("unterminated quote after image.tag")
	case '\'':
		for i := start + 1; i < len(raw); i++ {
			switch raw[i] {
			case '\'':
				if i+1 < len(raw) && raw[i+1] == '\'' {
					i++
					continue
				}
				return start + 1, i, nil
			case '\n':
				return 0, 0, errors.New("image.tag spans more than one line; refusing to edit it in place")
			}
		}
		return 0, 0, errors.New("unterminated quote after image.tag")
	}
	end := start
	for end < len(raw) && raw[end] != '\n' {
		end++
	}
	if i := commentIndex(raw[start:end]); i >= 0 {
		end = start + i
	}
	for end > start && (raw[end-1] == ' ' || raw[end-1] == '\t') {
		end--
	}
	return start, end, nil
}

// commentIndex is the offset of a YAML trailing comment in a plain
// scalar's line remainder, or -1. A "#" only starts a comment when
// preceded by a blank, which is why "sha-1#2" is not truncated.
func commentIndex(line []byte) int {
	for i := 1; i < len(line); i++ {
		if line[i] == '#' && (line[i-1] == ' ' || line[i-1] == '\t') {
			return i
		}
	}
	return -1
}
