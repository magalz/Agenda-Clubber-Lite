package bdd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func writeTempFeature(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp feature: %v", err)
	}
	return path
}

func TestParseFeatureFileValid(t *testing.T) {
	src := `# language: pt
# Feature comment
@autenticacao
Funcionalidade: Cadastro e confirmação de conta
  Fluxo inicial de entrada na plataforma.

  @BDD-01-01 @BDR-001 @BDR-002
  @coberto
  Cenário: Cadastro com coletivo novo
    Dado que a candidata acessa o formulário de cadastro
    Quando envia dados válidos com um coletivo novo
    Então o sistema enfileira o e-mail de confirmação
    E redireciona para a página de confirmação enviada
    Mas não ativa a conta ainda
`
	path := writeTempFeature(t, "01-auth.feature", src)
	spec, err := ParseFeatureFile(path)
	if err != nil {
		t.Fatalf("ParseFeatureFile() error = %v", err)
	}
	if spec.Language != "pt" {
		t.Errorf("Language = %q, want pt", spec.Language)
	}
	if spec.Title != "Cadastro e confirmação de conta" {
		t.Errorf("Title = %q", spec.Title)
	}
	if len(spec.Tags) != 1 || spec.Tags[0] != "@autenticacao" {
		t.Errorf("Tags = %v, want [@autenticacao]", spec.Tags)
	}
	if len(spec.Scenarios) != 1 {
		t.Fatalf("len(Scenarios) = %d, want 1", len(spec.Scenarios))
	}
	sc := spec.Scenarios[0]
	if sc.ID != "BDD-01-01" {
		t.Errorf("ID = %q, want BDD-01-01", sc.ID)
	}
	if sc.Status != "coberto" {
		t.Errorf("Status = %q, want coberto", sc.Status)
	}
	if len(sc.BDRTags) != 2 || sc.BDRTags[0] != "BDR-001" || sc.BDRTags[1] != "BDR-002" {
		t.Errorf("BDRTags = %v, want [BDR-001 BDR-002]", sc.BDRTags)
	}
	if len(sc.Steps) != 5 {
		t.Errorf("len(Steps) = %d, want 5", len(sc.Steps))
	}
}

func TestParseFeatureFileScenarioOutline(t *testing.T) {
	src := `# language: pt
Funcionalidade: Validação de acessos

  @BDD-03-01 @BDR-004 @pendente
  Esquema do Cenário: Ocultação da página do coletivo
    Dado um coletivo aprovado
    Quando <papel> acessa a página do coletivo
    Então recebe o código HTTP <status>

    Exemplos:
      | papel  | status |
      | Membro | 404    |
      | Admin  | 404    |
`
	path := writeTempFeature(t, "03-collectives.feature", src)
	spec, err := ParseFeatureFile(path)
	if err != nil {
		t.Fatalf("ParseFeatureFile() error = %v", err)
	}
	if len(spec.Scenarios) != 1 {
		t.Fatalf("len(Scenarios) = %d, want 1", len(spec.Scenarios))
	}
	sc := spec.Scenarios[0]
	if !sc.Outline {
		t.Error("Outline = false, want true")
	}
	if len(sc.Examples) != 1 {
		t.Fatalf("len(Examples) = %d, want 1", len(sc.Examples))
	}
	tbl := sc.Examples[0]
	if len(tbl.Headers) != 2 || tbl.Headers[0] != "papel" || tbl.Headers[1] != "status" {
		t.Errorf("Headers = %v, want [papel status]", tbl.Headers)
	}
	if len(tbl.Rows) != 2 || tbl.Rows[0][0] != "Membro" || tbl.Rows[1][1] != "404" {
		t.Errorf("Rows = %v", tbl.Rows)
	}
}

func TestParseFeatureFileMissingLanguage(t *testing.T) {
	src := `Funcionalidade: Sem idioma

  @BDD-01-01 @coberto
  Cenário: Teste
    Dado algo
    Quando ação
    Então resultado
`
	path := writeTempFeature(t, "no-lang.feature", src)
	if _, err := ParseFeatureFile(path); err == nil {
		t.Fatal("expected error for missing '# language: pt', got nil")
	}
}

func TestParseFeatureFileStatusTags(t *testing.T) {
	cases := []struct {
		name string
		tags string
	}{
		{name: "missing status", tags: "@BDD-01-01 @BDR-001"},
		{name: "duplicate status", tags: "@BDD-01-01 @coberto @pendente"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "# language: pt\nFuncionalidade: Teste\n\n  " + tc.tags + "\n  Cenário: Exemplo\n    Dado algo\n    Quando ação\n    Então resultado\n"
			path := writeTempFeature(t, "status.feature", src)
			if _, err := ParseFeatureFile(path); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestParseFeatureFileMissingSteps(t *testing.T) {
	cases := []struct {
		name  string
		steps string
	}{
		{name: "missing Dado", steps: "    Quando ação\n    Então resultado\n"},
		{name: "missing Quando", steps: "    Dado algo\n    Então resultado\n"},
		{name: "missing Então", steps: "    Dado algo\n    Quando ação\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "# language: pt\nFuncionalidade: Passos\n\n  @BDD-01-02 @coberto\n  Cenário: Incompleto\n" + tc.steps
			path := writeTempFeature(t, "steps.feature", src)
			if _, err := ParseFeatureFile(path); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestParseFeatureFileMisspelledScenarioKeyword(t *testing.T) {
	src := `# language: pt
Funcionalidade: Rejeição de linha desconhecida
  Descrição antes do primeiro cenário continua permitida.

  @BDD-01-01 @coberto
  Cenário: Primeiro cenário
    Dado algo
    Quando ação
    Então resultado

  @BDD-01-02 @coberto
  Cenario: X
    Dado outro estado
    Quando outra ação
    Então outro resultado
`
	path := writeTempFeature(t, "misspelled.feature", src)
	_, err := ParseFeatureFile(path)
	if err == nil {
		t.Fatal("expected error for misspelled 'Cenario: X', got nil")
	}
	if !strings.Contains(err.Error(), "misspelled.feature:12:") {
		t.Errorf("error = %q, want file and line 12", err.Error())
	}
}

func TestExtractLinkBoundary(t *testing.T) {
	w := &World{}
	body := "Acesse https://agenda.test/confirmar-email?token=errado para trocar."
	if got := w.ExtractLink(body, "/confirmar"); got != "" {
		t.Errorf("ExtractLink matched /confirmar-email for /confirmar: got %q", got)
	}
	if got := w.ExtractLink(body, "/confirmar-email"); got != "https://agenda.test/confirmar-email?token=errado" {
		t.Errorf("ExtractLink(/confirmar-email) = %q", got)
	}

	valid := "Links: https://agenda.test/confirmar-email?token=1 e https://agenda.test/confirmar?token=2."
	if got := w.ExtractLink(valid, "/confirmar"); got != "https://agenda.test/confirmar?token=2" {
		t.Errorf("ExtractLink(/confirmar) = %q, want token=2 link", got)
	}
}

var indexBDDIDPattern = regexp.MustCompile(`@?(BDD-\d{2}-\d{2})`)

func TestBDDFeatureSpecsValid(t *testing.T) {
	bddDir := filepath.Join("..", "..", "docs", "bdd")
	bdrDir := filepath.Join("..", "..", "docs", "bdr")
	indexPath := filepath.Join(bddDir, "INDEX.md")

	specs, err := LoadFeatureDir(bddDir)
	if err != nil {
		t.Fatalf("LoadFeatureDir(%s): %v", bddDir, err)
	}
	if len(specs) == 0 {
		t.Fatalf("no .feature files found in %s", bddDir)
	}

	seenIDs := make(map[string]string)
	specStatuses := make(map[string]string)
	for _, spec := range specs {
		for _, sc := range spec.Scenarios {
			if prev, exists := seenIDs[sc.ID]; exists {
				t.Errorf("duplicate scenario ID %s in %s (already defined in %s)", sc.ID, spec.Path, prev)
			}
			seenIDs[sc.ID] = spec.Path
			specStatuses[sc.ID] = sc.Status

			for _, bdr := range sc.BDRTags {
				matches, err := filepath.Glob(filepath.Join(bdrDir, bdr+"-*.md"))
				if err != nil {
					t.Fatalf("glob BDR %s: %v", bdr, err)
				}
				if len(matches) == 0 {
					t.Errorf("%s:%d: scenario %s references %s, which has no matching file in %s", spec.Path, sc.Line, sc.ID, bdr, bdrDir)
				}
			}
		}
	}

	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read %s: %v", indexPath, err)
	}
	indexStatuses := parseBDDIndex(t, string(indexData))

	for id, wantStatus := range specStatuses {
		gotStatus, ok := indexStatuses[id]
		if !ok {
			t.Errorf("scenario %s is missing from %s", id, indexPath)
			continue
		}
		if gotStatus != wantStatus {
			t.Errorf("scenario %s status in INDEX.md = %q, want %q", id, gotStatus, wantStatus)
		}
	}
	for id := range indexStatuses {
		if _, ok := specStatuses[id]; !ok {
			t.Errorf("orphan scenario %s in %s does not exist in any .feature file", id, indexPath)
		}
	}
}

func parseBDDIndex(t *testing.T, content string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		m := indexBDDIDPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		id := m[1]
		if _, exists := out[id]; exists {
			t.Errorf("duplicate %s row in docs/bdd/INDEX.md", id)
		}
		status := ""
		for _, cell := range strings.Split(line, "|") {
			token := strings.Trim(strings.TrimSpace(cell), "`@")
			switch token {
			case "coberto", "pendente", "browser":
				status = token
			}
		}
		out[id] = status
	}
	return out
}
