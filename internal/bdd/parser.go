package bdd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// FeatureSpec represents a parsed pt-BR Gherkin feature file.
type FeatureSpec struct {
	Path      string
	Language  string
	Title     string
	Tags      []string
	Scenarios []ScenarioSpec
}

// ScenarioSpec represents a parsed scenario or scenario outline.
type ScenarioSpec struct {
	ID       string
	Title    string
	Line     int
	Tags     []string
	Status   string
	BDRTags  []string
	Steps    []StepSpec
	Examples []ExampleTable
	Outline  bool
}

// StepSpec represents a single Gherkin step line.
type StepSpec struct {
	Keyword string
	Text    string
	Line    int
}

// ExampleTable represents an Exemplos table belonging to a scenario outline.
type ExampleTable struct {
	Headers []string
	Rows    [][]string
}

var (
	bddIDPattern  = regexp.MustCompile(`^@BDD-\d{2}-\d{2}$`)
	bdrTagPattern = regexp.MustCompile(`^@BDR-\d{3}$`)
	langPattern   = regexp.MustCompile(`^#\s*language:\s*(\S+)\s*$`)
)

var validStatusTags = map[string]string{
	"@coberto":  "coberto",
	"@pendente": "pendente",
	"@browser":  "browser",
}

// ParseFeatureFile parses and validates a single pt-BR Gherkin feature file.
func ParseFeatureFile(path string) (FeatureSpec, error) {
	f, err := os.Open(path)
	if err != nil {
		return FeatureSpec{}, fmt.Errorf("open feature %s: %w", path, err)
	}
	defer f.Close()

	spec := FeatureSpec{Path: path}
	var (
		pendingTags    []string
		inFeature      bool
		startedSection bool
		inBackground   bool
		inExamples     bool
		hasBgGiven     bool
		current        *ScenarioSpec
		currentTable   *ExampleTable
		lastStepKind   string
		lineNum        int
	)

	flushScenario := func() error {
		if current == nil {
			return nil
		}
		if currentTable != nil {
			current.Examples = append(current.Examples, *currentTable)
			currentTable = nil
		}
		if err := validateScenario(path, *current, hasBgGiven); err != nil {
			return err
		}
		spec.Scenarios = append(spec.Scenarios, *current)
		current = nil
		inExamples = false
		lastStepKind = ""
		return nil
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lineNum++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}

		if strings.HasPrefix(raw, "#") {
			if !inFeature {
				if m := langPattern.FindStringSubmatch(raw); m != nil {
					spec.Language = m[1]
				}
			}
			continue
		}

		if strings.HasPrefix(raw, "@") {
			tags, err := parseTagLine(path, lineNum, raw)
			if err != nil {
				return FeatureSpec{}, err
			}
			pendingTags = append(pendingTags, tags...)
			continue
		}

		if title, ok := cutPrefixTrim(raw, "Funcionalidade:"); ok {
			if inFeature {
				return FeatureSpec{}, fmt.Errorf("%s:%d: duplicate Funcionalidade declaration", path, lineNum)
			}
			if spec.Language != "pt" {
				return FeatureSpec{}, fmt.Errorf("%s:%d: missing '# language: pt' header before Funcionalidade", path, lineNum)
			}
			if title == "" {
				return FeatureSpec{}, fmt.Errorf("%s:%d: Funcionalidade title is empty", path, lineNum)
			}
			spec.Title = title
			spec.Tags = pendingTags
			pendingTags = nil
			inFeature = true
			continue
		}

		if !inFeature {
			return FeatureSpec{}, fmt.Errorf("%s:%d: unexpected content before Funcionalidade: %q", path, lineNum, raw)
		}

		if _, ok := cutPrefixTrim(raw, "Contexto:"); ok {
			if err := flushScenario(); err != nil {
				return FeatureSpec{}, err
			}
			startedSection = true
			inBackground = true
			inExamples = false
			lastStepKind = ""
			pendingTags = nil
			continue
		}

		if title, ok := cutPrefixTrim(raw, "Esquema do Cenário:"); ok {
			if err := flushScenario(); err != nil {
				return FeatureSpec{}, err
			}
			startedSection = true
			inBackground = false
			sc, err := newScenarioSpec(path, lineNum, title, pendingTags, true)
			if err != nil {
				return FeatureSpec{}, err
			}
			pendingTags = nil
			current = &sc
			continue
		}

		if title, ok := cutPrefixTrim(raw, "Cenário:"); ok {
			if err := flushScenario(); err != nil {
				return FeatureSpec{}, err
			}
			startedSection = true
			inBackground = false
			sc, err := newScenarioSpec(path, lineNum, title, pendingTags, false)
			if err != nil {
				return FeatureSpec{}, err
			}
			pendingTags = nil
			current = &sc
			continue
		}

		if _, ok := cutPrefixTrim(raw, "Exemplos:"); ok {
			if current == nil || !current.Outline {
				return FeatureSpec{}, fmt.Errorf("%s:%d: Exemplos outside Esquema do Cenário", path, lineNum)
			}
			if currentTable != nil {
				current.Examples = append(current.Examples, *currentTable)
			}
			currentTable = &ExampleTable{}
			inExamples = true
			pendingTags = nil
			continue
		}

		if strings.HasPrefix(raw, "|") {
			if inExamples && currentTable != nil {
				cells, err := parseTableRow(path, lineNum, raw)
				if err != nil {
					return FeatureSpec{}, err
				}
				if len(currentTable.Headers) == 0 {
					currentTable.Headers = cells
				} else {
					if len(cells) != len(currentTable.Headers) {
						return FeatureSpec{}, fmt.Errorf("%s:%d: table row has %d cells, want %d", path, lineNum, len(cells), len(currentTable.Headers))
					}
					currentTable.Rows = append(currentTable.Rows, cells)
				}
			}
			continue
		}

		if kw, text, ok := parseStepLine(raw); ok {
			if inBackground {
				if kw == "Dado" {
					hasBgGiven = true
				}
				continue
			}
			if current == nil {
				return FeatureSpec{}, fmt.Errorf("%s:%d: step %q outside scenario", path, lineNum, raw)
			}
			if inExamples {
				return FeatureSpec{}, fmt.Errorf("%s:%d: step %q after Exemplos", path, lineNum, raw)
			}
			if kw == "Dado" || kw == "Quando" || kw == "Então" {
				lastStepKind = kw
			} else if lastStepKind == "" {
				return FeatureSpec{}, fmt.Errorf("%s:%d: continuation step %q has no preceding Dado/Quando/Então", path, lineNum, kw)
			}
			current.Steps = append(current.Steps, StepSpec{
				Keyword: kw,
				Text:    text,
				Line:    lineNum,
			})
			continue
		}

		if startedSection {
			return FeatureSpec{}, fmt.Errorf("%s:%d: unrecognized line %q", path, lineNum, raw)
		}
	}
	if err := scanner.Err(); err != nil {
		return FeatureSpec{}, fmt.Errorf("read feature %s: %w", path, err)
	}

	if err := flushScenario(); err != nil {
		return FeatureSpec{}, err
	}
	if spec.Language != "pt" {
		return FeatureSpec{}, fmt.Errorf("%s: missing '# language: pt' header", path)
	}
	if !inFeature || spec.Title == "" {
		return FeatureSpec{}, fmt.Errorf("%s: missing Funcionalidade declaration", path)
	}
	if len(spec.Scenarios) == 0 {
		return FeatureSpec{}, fmt.Errorf("%s: feature has no scenarios", path)
	}
	return spec, nil
}

// LoadFeatureDir parses and validates every .feature file in dir in sorted order.
func LoadFeatureDir(dir string) ([]FeatureSpec, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read feature dir %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".feature") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)

	specs := make([]FeatureSpec, 0, len(names))
	for _, name := range names {
		spec, err := ParseFeatureFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func parseTagLine(path string, line int, raw string) ([]string, error) {
	fields := strings.Fields(raw)
	for _, f := range fields {
		if !strings.HasPrefix(f, "@") || len(f) < 2 {
			return nil, fmt.Errorf("%s:%d: invalid tag %q", path, line, f)
		}
	}
	return fields, nil
}

func newScenarioSpec(path string, line int, title string, tags []string, outline bool) (ScenarioSpec, error) {
	if title == "" {
		return ScenarioSpec{}, fmt.Errorf("%s:%d: scenario title is empty", path, line)
	}
	sc := ScenarioSpec{
		Title:   title,
		Line:    line,
		Tags:    slices.Clone(tags),
		Outline: outline,
	}
	for _, tag := range tags {
		if bddIDPattern.MatchString(tag) {
			if sc.ID != "" {
				return ScenarioSpec{}, fmt.Errorf("%s:%d: duplicate BDD ID tag %q (already %q)", path, line, tag, sc.ID)
			}
			sc.ID = strings.TrimPrefix(tag, "@")
			continue
		}
		if status, ok := validStatusTags[tag]; ok {
			if sc.Status != "" {
				return ScenarioSpec{}, fmt.Errorf("%s:%d: duplicate status tag %q (already %q)", path, line, tag, sc.Status)
			}
			sc.Status = status
			continue
		}
		if bdrTagPattern.MatchString(tag) {
			sc.BDRTags = append(sc.BDRTags, strings.TrimPrefix(tag, "@"))
		}
	}
	if sc.ID == "" {
		return ScenarioSpec{}, fmt.Errorf("%s:%d: scenario %q is missing a @BDD-XX-NN tag", path, line, title)
	}
	if sc.Status == "" {
		return ScenarioSpec{}, fmt.Errorf("%s:%d: scenario %s is missing a status tag (@coberto, @pendente, or @browser)", path, line, sc.ID)
	}
	return sc, nil
}

func validateScenario(path string, sc ScenarioSpec, hasBgGiven bool) error {
	hasGiven := hasBgGiven
	hasWhen := false
	hasThen := false
	for _, st := range sc.Steps {
		switch st.Keyword {
		case "Dado":
			hasGiven = true
		case "Quando":
			hasWhen = true
		case "Então":
			hasThen = true
		}
	}
	if !hasGiven {
		return fmt.Errorf("%s:%d: scenario %s is missing a Dado step", path, sc.Line, sc.ID)
	}
	if !hasWhen {
		return fmt.Errorf("%s:%d: scenario %s is missing a Quando step", path, sc.Line, sc.ID)
	}
	if !hasThen {
		return fmt.Errorf("%s:%d: scenario %s is missing an Então step", path, sc.Line, sc.ID)
	}
	if sc.Outline {
		if len(sc.Examples) == 0 {
			return fmt.Errorf("%s:%d: scenario outline %s has no Exemplos table", path, sc.Line, sc.ID)
		}
		for i, tbl := range sc.Examples {
			if len(tbl.Headers) == 0 || len(tbl.Rows) == 0 {
				return fmt.Errorf("%s:%d: scenario outline %s Exemplos table %d must have headers and at least one row", path, sc.Line, sc.ID, i+1)
			}
		}
	}
	return nil
}

func cutPrefixTrim(s, prefix string) (string, bool) {
	if !strings.HasPrefix(s, prefix) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(s, prefix)), true
}

func parseStepLine(raw string) (string, string, bool) {
	for _, kw := range []string{"Dado ", "Quando ", "Então ", "E ", "Mas "} {
		if strings.HasPrefix(raw, kw) {
			return strings.TrimSpace(kw), strings.TrimSpace(strings.TrimPrefix(raw, kw)), true
		}
	}
	return "", "", false
}

func parseTableRow(path string, line int, raw string) ([]string, error) {
	if !strings.HasPrefix(raw, "|") || !strings.HasSuffix(raw, "|") {
		return nil, fmt.Errorf("%s:%d: malformed table row %q", path, line, raw)
	}
	inner := raw[1 : len(raw)-1]
	parts := strings.Split(inner, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells, nil
}
