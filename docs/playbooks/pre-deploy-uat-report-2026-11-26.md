# Relatório de Execução UAT Pré-Deploy — Agenda Clubber Lite (2026-11-26 / 27)

- **Fase / Issue:** `ACL-126` (`ACL-129` — Wave 3)
- **Branch base testada:** `phase/ACL-126-uat-bdd-regras-negocio` (base `origin/main` `96e0ac8`, pós-merge do PR #7 com os 10 itens de SecOps)
- **Runbook executado:** [pre-deploy-uat-runbook.md](./pre-deploy-uat-runbook.md) (Suítes 0 a 11)
- **Ambiente isolado:**
  - `agenda-uat-mongo` (`mongo:8.0` digest pinado, `--network=host --port 27019 --replSet rs0`, banco `agenda_uat`, descartado ao final)
  - `agenda-uat-mailpit` (`axllent/mailpit:v1.33.1` digest pinado, `--network=host`, SMTP `:1025`, UI `http://localhost:8025`, removido ao final)
  - Binário nativo `cmd/agenda serve` em `http://localhost:8120` (`APP_ENV=dev`, `APP_ORIGIN=http://localhost:8120`, `APP_TZ=America/Fortaleza`), massa inicial `migrate` + `seed-dev` (`32 created, 0 existing`)
- **Veredito Geral:** **APROVADO FUNCIONALMENTE COM 5 CARDS DE POLIMENTO VISUAL/UX NO BACKLOG** (`0` erros críticos de segurança, `0` respostas `5xx`, `0` erros JS no console, `0` violações de CSP, `0` assets quebrados, `0` vazamentos de segredos em logs).

---

## 1. Métricas Não-Funcionais Observadas

| Métrica | Limiar do Runbook | Observado na Execução | Status |
|---|---|---|---|
| Total de requisições registradas em `uat-server.log` | — | `332` (`247` GET + `85` POST) | OK |
| Respostas HTTP `5xx` | `0` | `0` | **PASS** |
| Vazamento de segredos/cookies nos logs `slog` | `0` | `0` | **PASS** |
| Erros no Console JS (`console.error` / `pageerror`) | `0` | `0` em todas as telas e viewports | **PASS** |
| Violações de CSP (`securitypolicyviolation`) | `0` | `0` em todas as telas e viewports | **PASS** |
| Assets estáticos quebrados (`404` CSS/JS/fontes/SVG) | `0` | `0` | **PASS** |
| Latência máxima `GET` / `HTMX` | `<= 300 ms` | **`6 ms`** (pior caso: `GET /` = `6 ms`, `/eventos/:id` = `4 ms`, `/coletivos` = `4 ms`) | **PASS** |
| Latência máxima `POST` com Argon2id | `<= 1500 ms` | **`62 ms`** (pior caso: `POST /senha/trocar` = `62 ms`, `/senha/redefinir` = `44 ms`, `/senha/verificar` = `40 ms`) | **PASS** |
| E-mails entregues no Mailpit | 10/10 templates | **`22/22` mensagens cobrindo `10/10` templates** | **PASS** |
| Viewports responsivos sem overflow horizontal | `1280×800`, `768×1024`, `375×812` | `0` overflow horizontal nos 3 viewports | **PASS** |

---

## 2. Resultado Consolidado por Suíte (`Suíte 0` a `Suíte 11`)

| Suíte | Escopo Principal | Resultado | Observações / Cards Abertos |
|---|---|---|---|
| **Suíte 0** | `/healthz`, cabeçalhos CSP/XFO/XCTO/COOP, `404` trailing slash, `403` Origin/Sec-Fetch-Site, `413` `> 64 KiB`, `go.mod` (`x/crypto v0.59.0`, `go-mail v0.8.2`), identidade visual `/entrar` | **PASS** | Todos os cabeçalhos exatos, `404`/`403`/`413` validados, tema CircuitoNE íntegro (`0` erros). |
| **Suíte 1** | `/cadastro`, `phoneMask`, `passwordFields`, `collectivePicker` (existente e coletivo novo), anti-enumeração `/cadastro/enviado` | **PASS** | Todos os 3 widgets e a anti-enumeração validados; `verify_email` chegou com saudação sem nome `Olá!` (`ACL-48`). |
| **Suíte 2** | `GET/POST /confirmar` (idempotência de GET, uso único de POST), `/aguardando`, `admin_new_signup`, `/confirmar/reenviar` | **PASS** | Recarregamento de `GET /confirmar` preservou token; reuso retornou `400`; reenvio gerou novo `verify_email` sem nome. |
| **Suíte 3** | `/entrar` nos estados `pending_email`, `pending_approval`, senha incorreta, ativo (`__Host-acl_session`), `/sair`, `401` + `HX-Redirect: /entrar` | **PASS** | Todas as barreiras de estado, logout e contrato HTMX `401` + `HX-Redirect` validados. |
| **Suíte 4** | `.expiry-banner` (`senha-quase`), bloqueio `senha-vencida` → `/senha/trocar`, `POST /senha/verificar` (histórico 2 senhas), `/senha/esqueci` + `/senha/redefinir` | **PASS (1 achado UX)** | Fluxos e bloqueios 100% corretos. Achado UX: **`ACL-132`** (checklist em `/senha/redefinir` mantém 2 itens desmarcados no cliente). |
| **Suíte 5** | `/perfil` (`phoneMask`, `perfil-salvo`), `/perfil/email` (e-mail próprio `422`, ocupado anti-enum `existing_account`, novo `change_email` + `email_change_notice`), `/confirmar-email` (sobrevivência da sessão, códigos `perfilNotices`) | **PASS (2 achados UX)** | Fluxo 2 etapas e sobrevivência de sessão 100% corretos. Achados UX: **`ACL-133`** (`"$ $"` duplicado em rótulos de `/perfil`) e **`ACL-134`** (`#phone` sem máscara no GET inicial). |
| **Suíte 6** | `/coletivos`, `collectivePicker` join, `/coletivos/novo`, `/coletivo/:id` (aprovar/recusar pedido, promover, renomear, autorrebaixar `?msg=voce-rebaixado`), ocultação **`404`** para Membro e Admin | **PASS** | Ocultação `404` comprovada após autorrebaixamento e para `admin@agenda.test`; imutabilidade do Master preservada. |
| **Suíte 7** | `/admin` (`403` para não-Admin, `aprovar-tudo`, `conta/aprovar`, `pedido/aprovar`, `coletivo/recusar`, `conta/recusar` em cascata), `/admin/contas` (bloquear/desbloquear, auto-bloqueio ausente), `/admin/coletivos`, `/admin/historico` | **PASS** | Todas as ações e bloqueios validados; conta bloqueada recebeu `401` no login; auditoria preservou snapshots sem `@` no `<main>`. |
| **Suíte 8** | `/eventos/novo` (5 widgets: `datePicker`, `timeSpinner`, `durationSpinner`, `moneyInput`, `eventDynamic`), `/opcoes/*`, criação com `rel="noopener noreferrer nofollow ugc"`, edição de passado, concorrência otimista **`409`**, `/meus-eventos`, `EventsBlocked` (`delta` vs override `admin`), exclusão | **PASS (1 achado UX)** | Todos os 5 widgets, `409` concorrente, override Admin e auditoria passaram. Achado UX: **`ACL-136`** (`GET /eventos/:id` exibe `[→] EDITAR` para `delta` levando a `409` texto puro). |
| **Suíte 9** | `/` calendário, navegação mensal HTMX `/calendario` + `pushState`, filtros UF/cidade, modal `<dialog id="evento-sheet">` + `Escape` devolvendo foco, `Analisar Data` (`/eventos/dia`, `MESMA CIDADE`, aviso de filtro desatualizado) | **PASS (1 achado visual)** | Navegação HTMX, filtros, modal e `Analisar Data` funcionando; nota: data inválida `99/99/2026` desabilita botão no cliente (evita round-trip). Achado visual: **`ACL-131`** (seta duplicada nos `<select>`). |
| **Suíte 10** | Conferência integral dos **10 templates de e-mail** no Mailpit (`22` mensagens), `ACL-48` (`Olá!`), `dailyLoop` (`password_expiring` = 1), `0` `@` no `/admin/historico` | **PASS** | 10/10 templates capturados sem duplicatas (`ACL-38`/`ACL-37`/`ACL-32`). |
| **Suíte 11** | Responsividade em `1280×800`, `768×1024` e `375×812`, `#calendar-day` inline `< 640px`, drawer `shell` (`inert` em `<main>`, focus trap `Tab` em `#menu-lateral`, `Escape` restaurando foco), desempenho consolidado | **PASS** | Zero overflow horizontal em todos os viewports; `inert` e focus trap no drawer validados ao vivo. |

---

## 3. Defeitos Encontrados e Cards Abertos no ItsaPlan (`ACL` Backlog)

Todos os defeitos encontrados durante o UAT foram registrados imediatamente no Backlog do projeto `ACL` com a etiqueta `found-by-agent` (sem alterar código de produção nesta fase):

| Card | Prioridade | Tipo | Suíte / Rota | Resumo do Defeito |
|---|---|---|---|---|
| **ACL-131** | Média | Bug | Suítes 3, 8, 9 (`GET /`, `GET /eventos/novo`) | Selects (`#calendar-uf`, `#calendar-cidade` e selects de `/eventos/novo`) renderizam seta de dropdown duplicada (`1280×800` e `375×812`). |
| **ACL-132** | Baixa | Bug | Suíte 4 (`GET /senha/redefinir`) | Checklist de senha em `/senha/redefinir` mantém 2 itens (`diferente do seu e-mail` e `diferente das 2 últimas senhas`) desmarcados (`6/8`) no cliente para senha válida. |
| **ACL-133** | Baixa | Bug | Suíte 5 (`GET /perfil`) | Rótulos de campos em `/perfil` exibem prefixo terminal duplicado `"$ $"`. |
| **ACL-134** | Baixa | Bug | Suíte 5 (`GET /perfil`) | Campo `#phone` em `/perfil` exibe dígitos crus sem máscara `(XX) XXXXX-XXXX` no carregamento inicial. |
| **ACL-136** | Média | Bug | Suíte 8 (`GET /eventos/:id`) | Detalhe de evento em coletivo com `EventsBlocked=true` exibe botão `[→] EDITAR` para Administrador não-Admin (`delta`), levando a `HTTP 409` texto puro fora do shell. |

---

## 4. Limpeza Pós-Execução (Teardown)

- Processo temporário `agenda-uat.exe serve` encerrado.
- Container `agenda-uat-mailpit` removido (`podman rm -f`).
- Banco temporário `agenda_uat` descartado (`db.dropDatabase()`) e container `agenda-uat-mongo` removido após a suíte final de validação da Wave 4.
