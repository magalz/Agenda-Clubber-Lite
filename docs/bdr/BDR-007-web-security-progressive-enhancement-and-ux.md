---
id: BDR-007
title: "Segurança Web, Progressive Enhancement, Interatividade e Acessibilidade"
status: accepted
date: 2026-11-26
superseded_by: null
issues: [ACL-127]
scope: [internal/web, web/static/js/app.js, web/static/css, web/templates]
---

# BDR-007: Segurança Web, Progressive Enhancement, Interatividade e Acessibilidade

## Contexto de Negócio
O Agenda Clubber Lite é uma aplicação auto-hospedada sem etapa de build front-end, sem npm em runtime e sem CDNs externos, com identidade visual dark-only herdada do CircuitoNE. O produto exige defesa em profundidade no navegador (CSP estrita sem `unsafe-inline` nem `unsafe-eval`, proteção contra CSRF e abuso de payload), funcionamento integral mesmo sem JavaScript habilitado e experiência responsiva/acessível de nível aplicativo quando HTMX e Alpine.js estão ativos.

## Regra Acordada

### 1. CSP Estrita, Cabeçalhos de Segurança e Proteção HTTP
- **Content-Security-Policy (`internal/web/middleware.go`):** enviada em **toda** resposta (incluindo 404, 413 e pânicos recuperados):
  `default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`
  - Consequências obrigatórias nos templates: zero `<script>` inline com código, zero atributos `on*=`, zero atributos `style="..."`.
  - Alpine.js usa o **build CSP** (`alpine-csp-3.17.5.min.js`): componentes são registrados via `Alpine.data()` em `web/static/js/app.js`, e atributos de template referenciam apenas nomes de propriedades/métodos (sem negação, ternário, literais ou argumentos).
  - HTMX 2 (`htmx-2.0.11.min.js`) é configurado por `<meta name="htmx-config" content='{"allowEval":false,"allowScriptTags":false,"includeIndicatorStyles":false,"selfRequestsOnly":true,"historyCacheSize":0,"refreshOnHistoryMiss":true}'>`.
  - Dados JSON destinados a scripts, quando necessários, usam blocos `<script type="application/json" id="...">`.
- **Demais cabeçalhos de segurança:** `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, `Permissions-Policy: camera=(), microphone=(), geolocation=(), payment=()` e `Cross-Origin-Opener-Policy: same-origin`.
- **Mutações, CSRF e limites HTTP:**
  - Mutações são exclusivamente `POST`. O middleware `sameOrigin` executa **antes** da leitura de sessão e exige `Origin == APP_ORIGIN` (ausência também é recusada) e `Sec-Fetch-Site != "cross-site"`, respondendo **403 Forbidden** (`"origem não permitida"`).
  - Corpos de requisição são limitados a **64 KiB** (`maxBodyBytes = 64 << 10`): `Content-Length` acima do limite responde **413 Request Entity Too Large** (`"corpo grande demais"`), e corpos chunked passam por `http.MaxBytesReader`.
  - `RedirectTrailingSlash = false` no Gin: caminhos com barra final (ex.: `/healthz/`) respondem **404**, pois o redirect interno do Gin contornaria os cabeçalhos de segurança.
- **IP do cliente:** configurado via `r.SetTrustedProxies(TRUSTED_PROXY_CIDRS)` e `r.RemoteIPHeaders = []string{"CF-Connecting-IP"}`. `TrustedPlatform` nunca é definido.
- **Links externos de dados de usuário:** aceitam apenas `http`/`https` (`domain.TicketURLProblem` / `ticketHref`) e são renderizados com `rel="noopener noreferrer nofollow ugc"` e `target="_blank"`.
- **Arquivos estáticos:** `Assets` usa allowlist exata de caminhos embutidos (`web/embed.go`); `asset "path"` em template entra em pânico para nomes desconhecidos.

### 2. Progressive Enhancement (Sem JavaScript) e Contrato HTMX
- **Funcionamento sem JS:**
  - Todo formulário posta para o servidor e recebe redirecionamento **303 See Other** em sucesso ou **422** com valores preservados em erro.
  - No formulário de eventos, os selects dependentes (cidades de uma UF e subestilos de um estilo) possuem o botão secundário `[↻] ATUALIZAR LISTAS` (`name="acao" value="atualizar"`), que re-renderiza o formulário com `200 OK` e as listas preenchidas sem validar nem salvar. Com JS ativo, o botão e sua dica ficam ocultos (reaparecendo apenas se o `fetch` de `/opcoes/*` falhar).
  - No calendário, a barra de meses, o formulário GET de filtro (com botão `FILTRAR`, oculto por `.calendar-page--js`), os links de dias e os chips de evento são links HTML comuns.
- **Contrato HTMX (`HX-Request: true`):**
  - Sessão ausente/expirada ou senha expirada responde **401 Unauthorized** com cabeçalho `HX-Redirect` (`denyTo`), e ações bem-sucedidas que redirecionam respondem **204 No Content** com `HX-Redirect` (`redirect`), evitando que o HTMX siga um 303 e injete a página de destino dentro de um fragmento.
  - Páginas (`a.page`) e fragmentos HTMX (`/calendario`, `/eventos/dia`, `/eventos/:id`, `/opcoes/*`) enviam `Cache-Control: no-store`.
  - O painel `Analisar Data` (`#analise-resultado`) libera a troca de respostas **422** via listener `htmx:beforeSwap` em `app.js`, restrito exclusivamente a `#analise-resultado` e status `422`; qualquer outro 4xx permanece sem swap. Falhas de rede/servidor exibem mensagem fixa (`MSG_DAY_FAILED`) com `role="alert"`.
  - Chamadas `fetch` fora do HTMX (`refill` em `/opcoes/*` e `collectivePicker` em `/coletivos/sugestoes`) usam `redirect: 'error'` para que redirecionamentos inesperados (ex.: sessão expirada → `/entrar`) falhem em vez de preencher selects ou listboxes com HTML de login.

### 3. Concorrência e Navegação do Calendário (`calendarPage`)
- O root (`x-data="calendarPage"`) marca `.calendar-page--js` e envolve `#calendar-slot` (que recebe apenas o fragmento `GET /calendario`) e `<dialog id="evento-sheet">` (mantido fora do slot para não perder estado nem listeners delegados). Apenas cliques simples com o botão esquerdo são interceptados; teclas modificadoras, botão do meio ou falhas seguem o link nativo.
- **Última escolha sempre vence:** toda mudança de mês/filtro parte de `latest()` (o endereço mais recente já solicitado), nunca dos controles atualmente no DOM. Cada tipo de requisição (mês, chip/sheet, lista do dia) roda em sua própria fila `newestOnly` (uma em voo, apenas a mais nova aguarda) e com seu próprio elemento `source` no HTMX (`#calendar-slot`, `#evento-sheet`, root), evitando colisões de fila interna do HTMX.
- **Recuperação de travamento:** qualquer requisição que exceda **`HANG_WAIT` (8000 ms)** entrega a navegação ao link nativo da escolha mais recente (`location.replace` / `location.assign`).
- **Histórico, transições e responsividade:** cada mês escolhido entra no histórico via `history.pushState`; `popstate` recarrega sem novo push validando `uf`/`cidade` contra os selects presentes (`offers`). Transições deslizantes (`startViewTransition`) são puladas se `#evento-sheet` estiver aberto ou se `prefers-reduced-motion: reduce` estiver ativo. Abaixo de **640px (`WIDE`)**, clicar no número do dia carrega a lista do dia em `#calendar-day`; a partir de 640px segue o link para `/eventos/dia`. Meses fora do intervalo representável por `dayOf` (anos `< 0100` ou `> 9999`) delegam ao link nativo.

### 4. Os 10 Componentes `Alpine.data` (`web/static/js/app.js`)
1. `shell`: controla a gaveta lateral móvel (`#menu-lateral`) abaixo de `1024px` (`DESKTOP`), alterna `:aria-expanded`, `:inert="mainInert"` no `<main>` e prende o foco `Tab`/`Shift+Tab` enquanto aberta.
2. `phoneMask`: aplica máscara `(00) 0000-0000` / `(00) 00000-0000` preservando o cursor por dígito e removendo prefixo `+55` apenas em colagem/autofill.
3. `passwordFields`: atualiza em tempo real o checklist de política de senha (alinhado a `auth.CheckPolicy`, Unicode-aware, sem tocar na regra `history` que pertence ao servidor) e o status de confirmação (`matchText`).
4. `collectivePicker`: combobox acessível sobre `/coletivos/sugestoes` (debounce 250ms, mínimo 2 caracteres); no cadastro, nome sem sugestão selecionada força `Administrador` e desabilita `Membro`; em `data-mode="join"` (`/coletivos`), atua apenas para entrada em coletivo existente.
5. `datePicker`: calendário popup (`role="dialog"`, tabela `.calendar__grid` com `role="grid"`) sobre `#date`, respeitando `data-today` e `data-keep` (edita data passada original) com dias indisponíveis em `aria-disabled="true"` e navegação por setas/`PageUp`/`PageDown`.
6. `timeSpinner`: spinner `HH:MM` com máscara, botões `▲▼`, setas (1 min) e `PageUp`/`PageDown` (1h), preservando o cursor via `caretAfterRewrite` ao editar no meio do campo.
7. `durationSpinner`: spinners coordenados de horas (`0`–`23`) e minutos (`0`–`59`), respeitando `15m`–`23h59` com ajuste mínimo para `0h15` ao sair do campo.
8. `moneyInput`: máscara BRL (dígitos interpretados como centavos até `R$ 99.999,99`) com `caretAfterRewrite` e sincronização com o checkbox `#free` (limpa e desabilita `#price`).
9. `eventDynamic`: orquestra cascatas `state → city` e `style → substyle` via `refill`, exibição condicional de `#type_other`/`#style_other` (`Outros`), desativação de `#substyle` quando estilo é `Outros` e acionamento do painel `Analisar Data` com aviso de desatualização (`panelStale`).
10. `calendarPage`: gerencia o calendário interativo descrito na seção 3.

### 5. Acessibilidade, Identidade Visual CircuitoNE e Allowlists de Avisos (`*Notices`)
- **Acessibilidade:**
  - *Focus trap* na gaveta móvel (`shell.onDrawerKeydown`) + `:inert="mainInert"` no `<main>`; retorno de foco ao botão toggle ao fechar por `Esc`/clique fora e retorno ao chip (ou ao dia com `tabindex="0"`) ao fechar `#evento-sheet`.
  - *Roving tabindex* nos links de dia do calendário (setas, `Home`, `End`) e na grade do `datePicker`.
  - Anel de foco padrão `outline: 2px solid #fff; outline-offset: 2px`, com a **única exceção** `outline-offset: -2px` em `.date-picker__day:focus-visible` para não invadir células vizinhas.
  - Alvos interativos `>= 24px` (WCAG 2.5.8), `scroll-padding-top: 5rem` (WCAG 2.4.11) e desativação/encurtamento de animações sob `prefers-reduced-motion: reduce` (nenhuma animação acima de 5s).
- **Identidade visual CircuitoNE (dark-only, `web/static/css/tokens.css`):**
  - Cores literais centralizadas em `tokens.css`: `--color-bg: #050506`, `--color-bg-elev: #0b0b0d`, `--color-surface: #101014`, `--color-foreground: #e8e8ea`, `--color-muted: #8a8a92`, `--color-accent: #ff2040`, `--color-accent-text: #ff7285`, `--color-ok: #35e06f`, `--color-warn: #ffcc33`, `--color-line: #23232a`, `--color-control: #6b6b75`, `--color-focus: #ffffff`.
  - Fontes auto-hospedadas (`JetBrains Mono` para corpo, `Space Mono` para títulos/wordmark, pesos 400 e 700 apenas), cantos retos (`border-radius: 0`), grade CRT 40px, scanlines, microcopy de terminal (`$ `, `[erro]`, `[✕]`, `[→]`), wordmark `◢◤ AGENDA_CLUBBER` e rodapé `$ feito_por --dev=IgnisDev` + `contato: ignisdev@magalz.space`.
- **Allowlists fixas de avisos (`?msg=`):** códigos de notificação após redirect só exibem texto se constarem nas tabelas fixas do servidor — `calendarNotices`, `eventNotices`, `adminNotices`, `manageAccountNotices`, `manageCollectiveNotices`, `collectiveNotices`, `collectivesNotices` e `perfilNotices` — nunca ecoando parâmetros desconhecidos da URL.

## Exceções e Casos de Borda
- **`.calendar__cell--today` com `outline-offset: -2px`:** não se trata de anel de foco, mas do contorno de destaque (`--accent`) que identifica visualmente a célula de hoje por dentro da borda.
- **Mudança de viewport com gaveta aberta (`shell`):** ao cruzar para `>= 1024px`, a gaveta fecha automaticamente sem mover foco para o botão toggle (que passa a ficar oculto).
- **Cidade do estado antigo em `calendarPage`:** se o usuário escolher uma cidade enquanto uma troca de UF ainda está em voo, `onCalendarChange` ignora o evento obsoleto para não misturar estados e cidades.
- **Variável CSS `--i` para stagger:** definida via CSSOM (`element.style.setProperty('--i', ...)`) em vez de atributo `style="..."` no HTML, preservando a conformidade com a CSP `style-src 'self'`.
- **Destaque de avisos de erro em `/perfil`:** em `perfilNotices`, códigos positivos (`perfil-salvo`, `email-pendente`, `email-confirmado`) renderizam `.status--ok`, enquanto `email-invalido` e `email-em-uso` renderizam `.status--error`.

## Prova Executável (BDD)
- **CSP, cabeçalhos, limites de corpo, origem, trailing slash e IP (`internal/web`):**
  - `internal/web/middleware_test.go`: `TestContentSecurityPolicyConstant`, `TestSecurityHeadersOnEveryResponse`, `TestLimitBody`
  - `internal/web/auth_test.go`: `TestSameOrigin`, `TestSameOriginInTheRouter`, `TestSameOriginRunsBeforeTheSession`, `TestPagesAreNotStored`, `TestRedirect`, `TestSafeNext`, `TestParseForm`
  - `internal/web/app_test.go`: `TestUnknownRouteIs404`, `TestPanicIsRecovered`, `TestRequestLogFields`, `TestClientIP`
  - `internal/web/render_test.go`: `TestHTMXConfigMetaIsExact`, `TestFooter`, `TestRenderedHTMLIsCSPSafe`, `TestShippedBaseRendersWithTheEmbeddedAssets`
  - `internal/web/assets_test.go`: `TestAssetsURLPanicsForNamesThatAreNotServed`, `TestStaticRoutesAreGetAndHeadOnly`
- **Shell, acessibilidade, widgets e formulário dinâmico (`internal/web`):**
  - `internal/web/layouts_test.go`: `TestAlpineAttributesNameRegisteredMembers`, `TestAppShellBindsMainInertOnMain`, `TestAppShellDerivesMainInertFromOpenAndBreakpoint`, `TestAppShellTrapsTabInsideTheDrawer`, `TestWordmarkContract`, `TestCheckAndRadioInputsMeetThe24pxTarget`
  - `internal/web/event_widgets_test.go`: `TestEwNoInlineScriptOrStyle`, `TestEwPostedNamesAreUnchanged`, `TestEwMarkup`, `TestEwDatePickerUsesTheCalendarGrid`, `TestEwDatePickerDates`, `TestEwTimeSpinnerKeepsCaret`, `TestEwMoneyInputKeepsCaret`, `TestEwNoThirdWidgetTouchesTheCaret`
  - `internal/web/event_dynamic_test.go`: `TestDynWrapsTheFormAndThePanel`, `TestDynMarkup`, `TestDynRefusedFormKeepsTheHooks`, `TestDynComponentMembers`, `TestDynScriptTalksToTheRoutes`, `TestDynRoutesAnswerTheScript`
  - `internal/web/signup_ui_test.go`: `TestSignupUIMarkup`, `TestSignupUIScript`, `TestSignupSuggestionRedirect`
  - `internal/web/routes_options_test.go`: `TestOptionsCities`, `TestOptionsSubstyles`, `TestOptionsFragment`, `TestOptionsFormListsAreTheServedLists`
- **Concorrência do calendário e allowlists de avisos (`internal/web`):**
  - `internal/web/calendar_ui_test.go`: `TestCuiSheetSitsOutsideTheMonthWithOnlyTheCloseButtonInTheBar`, `TestCuiMonthSwapsIntoAStableSlot`, `TestCuiChipsStayPlainLinks`, `TestCuiFilterButtonStaysForThePlainForm`, `TestCuiAlpineAttributesNameMembersOfCalendarPage`, `TestCuiEveryKindOfRequestNamesASourceOfItsOwn`, `TestCuiMonthChangesAreBuiltFromTheNewestAddress`, `TestCuiRequestsOfAKindRunOneAtATimeAndTheNewestWins`, `TestCuiSlideAndFocusAreSettledWhenTheAnswerComes`, `TestCuiScriptHandlesTheKeysAndTheBreakpoints`, `TestCuiNoInlineScriptOrStyle`, `TestCuiCityOfTheOldStateIsDropped`, `TestCuiPopStateTakesOnlyValuesThePageOffers`, `TestCuiSheetIsCheckedAtSwapTimeNotAtRequestTime`, `TestCuiAddMonthsReturnsNullForAnUnbuildableDay`, `TestCuiMonthBarHandsAnInvalidMonthToThePlainLink`
  - `internal/web/routes_calendar_test.go`: `TestCalendarNotice`
  - `internal/web/routes_events_test.go`: `TestEvdDetailNotices`, `TestEvdTicketLink`, `TestEvdTicketHref`
  - `internal/web/routes_admin_test.go`: `TestAdminNoticesComeFromFixedCodes`
  - `internal/web/routes_admin_manage_test.go`: `TestMngNoticesComeFromFixedCodes`
  - `internal/web/routes_collective_test.go`: `TestColetivoNoticeCodes`
  - `internal/web/routes_collectives_test.go`: `TestColetivosMessageCodes`
  - `internal/web/routes_profile_test.go`: `TestProfileNoticesComeFromTheFixedTable`
- **Rastreabilidade planejada (BDD / UAT):** `@BDD-07-01` a `@BDD-07-12`, `UAT-S7-01` a `UAT-S7-08`.
