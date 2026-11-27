# Runbook de UAT Pré-Deploy — Agenda Clubber Lite

- **Projeto:** Agenda Clubber Lite (`github.com/magalz/agenda-clubber-lite`)
- **Tarefa / Issue:** `ACL-127` / `T1.1`
- **Idioma da interface e do runbook:** Português do Brasil (`pt-BR`)
- **Escopo:** Cobertura determinística de 100% da superfície HTTP (**61 handlers / 50 caminhos únicos**, incluindo saúde e estáticos), **10 componentes `Alpine.data` (CSP build)** + fluxos **HTMX 2**, **10 templates de e-mail transacional**, **3 viewports responsivos**, **progressive enhancement sem JavaScript**, **fronteiras de segurança** e todos os **10 itens de SecOps** integrados no PR #7.

---

## 1. Pré-requisitos de Ambiente Isolado (Stack UAT)

Execute toda a bateria em uma stack **completamente isolada** do ambiente de desenvolvimento e de produção, sem depender de serviços externos.

### 1.1 Serviços e Variáveis de Ambiente

| Serviço / Processo | Imagem / Binário | Porta / Endereço | Configuração Obrigatória |
|---|---|---|---|
| `agenda-uat-mongo` | `mongo:8.0` (digest pinado de `deploy/compose.dev.yml`) | `127.0.0.1:27019` | `--replSet rs0`, banco `agenda_uat`, `MONGO_URI="mongodb://127.0.0.1:27019/?directConnection=true"`, `MONGO_DB="agenda_uat"` |
| `agenda-uat-mailpit` | `axllent/mailpit:v1.33.1` | SMTP `127.0.0.1:1025` / Web UI `http://127.0.0.1:8025` | Captura 100% dos e-mails transacionais; nenhum e-mail sai da máquina |
| `agenda` (`cmd/agenda`) | `go run ./cmd/agenda` ou binário compilado | `http://localhost:8120` (**nunca** `127.0.0.1` no navegador) | `APP_ENV=dev`, `APP_ADDR=:8120`, `APP_ORIGIN=http://localhost:8120`, `APP_TZ=America/Fortaleza`, `SMTP_HOST=127.0.0.1`, `SMTP_PORT=1025`, `SMTP_FROM="IgnisDev <ignisdev@magalz.space>"`, `SEED_PASSWORD="<senha-forte-valida>"` |

> **Atenção crítica ao hostname (`localhost` vs `127.0.0.1`):** O cookie de sessão usa prefixo `__Host-acl_session` com atributo `Secure` em todos os ambientes (`internal/web/auth.go`). Navegadores consideram `http://localhost:8120` um contexto seguro (*potentially trustworthy origin*), permitindo gravar cookies `Secure` em HTTP local; além disso, o middleware `sameOrigin()` exige igualdade estrita com `APP_ORIGIN=http://localhost:8120`. **Sempre acesse via `http://localhost:8120`.**

### 1.2 Bootstrap Determinístico

1. Subir `agenda-uat-mongo` na porta `27019` com `--replSet rs0` e inicializar o replica set (`rs.initiate({_id:'rs0', members:[{_id:0, host:'127.0.0.1:27019'}]})`) até `isWritablePrimary === true`.
2. Subir `agenda-uat-mailpit` expondo SMTP `:1025` e Web UI/API `:8025`.
3. Aplicar schema e índices:
   ```bash
   agenda migrate
   ```
   - **Esperado:** saída `schema ready` e código de saída `0`.
4. Popular a massa inicial (`seed-dev`):
   ```bash
   agenda seed-dev
   ```
   - **Esperado:** criação das 10 contas, 4 coletivos, 7 vínculos e 11 eventos de exemplo; anotar o link de confirmação impresso no stdout para `pendente-email@agenda.test`.
5. Iniciar o servidor web:
   ```bash
   agenda serve
   ```
   - **Esperado:** logs estruturados indicando `database ready`, `mail: sending through SMTP`, `daily: password reminders sent`, `daily: unconfirmed signups purged` e `starting addr=:8120 env=dev origin=http://localhost:8120`.
6. Confirmar liveness via CLI:
   ```bash
   agenda healthcheck
   ```
   - **Esperado:** código de saída `0`.

### 1.3 Personas de Teste

#### Contas da Massa Inicial (`seed-dev`, domínio `@agenda.test`, senha `SEED_PASSWORD`)

| Persona | E-mail | Estado / Papel | Uso Principal no UAT |
|---|---|---|---|
| **Admin** | `admin@agenda.test` | `active`, `IsAdmin=true` | Fila `/admin`, contas, coletivos/bloqueios, auditoria, override de edição/exclusão de eventos |
| **Administrador (Master)** | `dono@agenda.test` | `active`, Master/Administrador aprovado de `Coletivo Alfa` e `Produtora Beta` | Gestão de `/coletivo/:id`, criação/edição/exclusão de eventos, `Meus Eventos` |
| **Membro** | `membro@agenda.test` | `active`, Membro aprovado em `Coletivo Alfa` | Visualização do calendário, restrições de leitura (403 em criação, 404 em `/coletivo/:id`), promoção/rebaixamento |
| **Pendente de E-mail** | `pendente-email@agenda.test` | `pending_email` (link impresso pelo `seed-dev`) | Confirmação de e-mail, login bloqueado com oferta de reenvio |
| **Pendente de Aprovação** | `pendente-aprovacao@agenda.test` | `pending_approval`, pedido em `Coletivo Alfa` (Membro) | Login bloqueado aguardando admin, decisão na fila `/admin` |
| **Senha Vencida** | `senha-vencida@agenda.test` | `active`, senha com 92 dias (histórico contém `Anterior-<SEED_PASSWORD>`) | Bloqueio por `requireFreshPassword` → `/senha/trocar`, validação de histórico |
| **Senha Quase Vencendo** | `senha-quase@agenda.test` | `active`, senha com 87 dias (restam 3 dias) | Banner `.expiry-banner` no shell e lembrete `password_expiring` no job diário |
| **Novo Coletivo** | `novo-coletivo@agenda.test` | `pending_approval`, criou `Coletivo Gama` (`pending`) | Aprovação completa (`aprovar-tudo`) ou separada na fila `/admin` |
| **Pedido Ativo** | `pedido@agenda.test` | `active`, pedido pendente em `Produtora Beta` (Membro) | Fluxo de dupla aprovação (Admin + Administrador de `Produtora Beta`) |
| **Delta (Bloqueado)** | `delta@agenda.test` | `active`, Master de `Coletivo Delta` (`EventsBlocked=true`) | Matriz 403/409 de eventos bloqueados e override do Admin |

#### Contas Destrutivas Dedicadas (`uat-*@agenda.test`)

Para testes destrutivos (lockout de 15 min, recusa/exclusão de cadastro, troca de e-mail, bloqueio de conta, concorrência 409 e rate limit), crie e utilize exclusivamente contas dedicadas com prefixo `uat-` (ex.: `uat-signup1@agenda.test`, `uat-lockout@agenda.test`, `uat-email-old@agenda.test`, `uat-email-new@agenda.test`, `uat-reject@agenda.test`), preservando as contas do `seed-dev` para os demais cenários.

### 1.4 Limiares Não-Funcionais (Critérios de Aceite Globais)

| Métrica | Limiar Obrigatório |
|---|---|
| Erros no Console JavaScript (`console.error` / `pageerror`) | `0` em toda a execução |
| Violações de CSP (`securitypolicyviolation`) | `0` em toda a execução |
| Respostas HTTP `5xx` ou assets estáticos quebrados (`404` em CSS/JS/fontes/SVG) | `0` |
| Latência de rotas `GET` e fragmentos `HTMX` (local) | `<= 300 ms` |
| Latência de rotas `POST` com Argon2id (`/entrar`, `/cadastro`, `/senha/trocar`, `/senha/redefinir`, `/senha/verificar`) | `<= 1500 ms` |
| Consistência de fuso horário e formatação | `America/Fortaleza`, datas `dd/mm/aaaa`, hora 24h `HH:MM`, moeda `R$ 1.234,56` |

### 1.5 Política de Defeitos

Ao encontrar qualquer divergência em relação ao resultado esperado:
1. **Não interrompa** a execução das demais suítes (salvo bloqueio total de infraestrutura).
2. Abra imediatamente um card no Backlog do projeto `ACL` no **ItsaPlan** com a label `found-by-agent`, redigido em **português do Brasil (`pt-BR`)**, contendo:
   - ID do caso (`UAT-S<N>-<NN>`), rota e persona;
   - Passos exatos de reprodução e payload;
   - Resultado observado vs. resultado esperado;
   - Evidência (status HTTP, snippet de log/console ou mensagem do Mailpit).
3. Registre o ID do card no relatório de execução e **prossiga** para o próximo caso.

---

## 2. Inventário de Rastreabilidade (100% de Cobertura)

### 2.1 Rotas HTTP (61 Handlers / 50 Caminhos Únicos)

| Grupo / Arquivo | Método e Caminho | Acesso | Suíte |
|---|---|---|---|
| Infra (`app.go`) | `GET /healthz` | Público (sem sessão) | Suíte 0 |
| Estáticos (`app.go`) | `GET /static/*filepath` | Público (sem sessão) | Suíte 0 |
| Estáticos (`app.go`) | `HEAD /static/*filepath` | Público (sem sessão) | Suíte 0 |
| Cadastro (`routes_signup.go`) | `GET /cadastro` | Público | Suíte 1 |
| Cadastro (`routes_signup.go`) | `POST /cadastro` | Público | Suíte 1 |
| Cadastro (`routes_signup.go`) | `GET /cadastro/enviado` | Público | Suíte 1 |
| Sugestões (`routes_signup.go`) | `GET /coletivos/sugestoes` | Público | Suíte 1, 6 |
| Confirmação (`routes_confirm.go`) | `GET /confirmar` | Público | Suíte 2 |
| Confirmação (`routes_confirm.go`) | `POST /confirmar` | Público | Suíte 2 |
| Confirmação (`routes_confirm.go`) | `POST /confirmar/reenviar` | Público | Suíte 2 |
| Espera (`routes_confirm.go`) | `GET /aguardando` | Público | Suíte 2 |
| Login (`routes_login.go`) | `GET /entrar` | Público | Suíte 3 |
| Login (`routes_login.go`) | `POST /entrar` | Público | Suíte 3 |
| Logout (`routes_login.go`) | `POST /sair` | Autenticado (permite senha vencida) | Suíte 3, 4 |
| Recuperação (`routes_reset.go`) | `GET /senha/esqueci` | Público | Suíte 4 |
| Recuperação (`routes_reset.go`) | `POST /senha/esqueci` | Público | Suíte 4 |
| Recuperação (`routes_reset.go`) | `GET /senha/redefinir` | Público | Suíte 4 |
| Recuperação (`routes_reset.go`) | `POST /senha/redefinir` | Público | Suíte 4 |
| Senha (`routes_password.go`) | `GET /senha/trocar` | Autenticado (permite senha vencida) | Suíte 4 |
| Senha (`routes_password.go`) | `POST /senha/trocar` | Autenticado (permite senha vencida) | Suíte 4 |
| Senha (`routes_password.go`) | `POST /senha/verificar` | Autenticado (permite senha vencida) | Suíte 4 |
| Perfil (`routes_profile.go`) | `GET /perfil` | Autenticado (senha válida) | Suíte 5 |
| Perfil (`routes_profile.go`) | `POST /perfil` | Autenticado (senha válida) | Suíte 5 |
| Perfil (`routes_profile.go`) | `POST /perfil/email` | Autenticado (senha válida) | Suíte 5 |
| Troca E-mail (`routes_profile.go`) | `GET /confirmar-email` | Público | Suíte 5 |
| Troca E-mail (`routes_profile.go`) | `POST /confirmar-email` | Público | Suíte 5 |
| Coletivos (`routes_collectives.go`) | `GET /coletivos` | Autenticado | Suíte 6 |
| Coletivos (`routes_collectives.go`) | `POST /coletivos/entrar` | Autenticado | Suíte 6 |
| Coletivos (`routes_collectives.go`) | `POST /coletivos/novo` | Autenticado | Suíte 6 |
| Coletivo (`routes_collective.go`) | `GET /coletivo/:id` | Administrador aprovado do coletivo (senão 404) | Suíte 6 |
| Coletivo (`routes_collective.go`) | `POST /coletivo/:id/pedido/aprovar` | Administrador aprovado do coletivo (senão 404) | Suíte 6 |
| Coletivo (`routes_collective.go`) | `POST /coletivo/:id/pedido/recusar` | Administrador aprovado do coletivo (senão 404) | Suíte 6 |
| Coletivo (`routes_collective.go`) | `POST /coletivo/:id/membro/promover` | Administrador aprovado do coletivo (senão 404) | Suíte 6 |
| Coletivo (`routes_collective.go`) | `POST /coletivo/:id/membro/rebaixar` | Administrador aprovado do coletivo (senão 404) | Suíte 6 |
| Coletivo (`routes_collective.go`) | `POST /coletivo/:id/nome` | Administrador aprovado do coletivo (senão 404) | Suíte 6 |
| Admin Fila (`routes_admin.go`) | `GET /admin` | Admin (`IsAdmin=true`, senão 403) | Suíte 7 |
| Admin Fila (`routes_admin.go`) | `POST /admin/pessoa/aprovar-tudo` | Admin | Suíte 7 |
| Admin Fila (`routes_admin.go`) | `POST /admin/conta/aprovar` | Admin | Suíte 7 |
| Admin Fila (`routes_admin.go`) | `POST /admin/conta/recusar` | Admin | Suíte 7 |
| Admin Fila (`routes_admin.go`) | `POST /admin/coletivo/aprovar` | Admin | Suíte 7 |
| Admin Fila (`routes_admin.go`) | `POST /admin/coletivo/recusar` | Admin | Suíte 7 |
| Admin Fila (`routes_admin.go`) | `POST /admin/pedido/aprovar` | Admin | Suíte 7 |
| Admin Fila (`routes_admin.go`) | `POST /admin/pedido/recusar` | Admin | Suíte 7 |
| Admin Gestão (`routes_admin_manage.go`) | `GET /admin/contas` | Admin | Suíte 7 |
| Admin Gestão (`routes_admin_manage.go`) | `POST /admin/conta/bloquear` | Admin | Suíte 7 |
| Admin Gestão (`routes_admin_manage.go`) | `POST /admin/conta/desbloquear` | Admin | Suíte 7 |
| Admin Gestão (`routes_admin_manage.go`) | `GET /admin/coletivos` | Admin | Suíte 7 |
| Admin Gestão (`routes_admin_manage.go`) | `POST /admin/coletivo/bloqueios` | Admin | Suíte 7 |
| Admin Gestão (`routes_admin_manage.go`) | `GET /admin/historico` | Admin | Suíte 7, 10 |
| Eventos (`routes_event_form.go`) | `GET /eventos/novo` | Administrador de coletivo não bloqueado | Suíte 8 |
| Eventos (`routes_event_form.go`) | `POST /eventos/novo` | Administrador de coletivo não bloqueado | Suíte 8 |
| Eventos (`routes_event_form.go`) | `GET /eventos/:id/editar` | Administrador do coletivo ou Admin | Suíte 8 |
| Eventos (`routes_event_form.go`) | `POST /eventos/:id/editar` | Administrador do coletivo ou Admin | Suíte 8 |
| Eventos (`routes_events.go`) | `GET /eventos/:id` | Autenticado (HTML completo ou fragmento HTMX) | Suíte 8, 9 |
| Eventos (`routes_events.go`) | `POST /eventos/:id/excluir` | Administrador do coletivo ou Admin | Suíte 8 |
| Meus Eventos (`routes_my_events.go`) | `GET /meus-eventos` | Autenticado | Suíte 8 |
| Opções (`routes_options.go`) | `GET /opcoes/cidades` | Autenticado (`Cache-Control: no-store`) | Suíte 8 |
| Opções (`routes_options.go`) | `GET /opcoes/subestilos` | Autenticado (`Cache-Control: no-store`) | Suíte 8 |
| Calendário (`routes_calendar.go`) | `GET /` | Autenticado | Suíte 9 |
| Calendário (`routes_calendar.go`) | `GET /calendario` | Autenticado (`Cache-Control: no-store`) | Suíte 9 |
| Analisar Data (`routes_day.go`) | `GET /eventos/dia` | Autenticado (página ou fragmento HTMX) | Suíte 8, 9 |

### 2.2 Componentes `Alpine.data` (10 Componentes CSP)

| # | Componente | Arquivo / Template | Suíte |
|---|---|---|---|
| 1 | `shell` | `web/static/js/app.js`, `layouts/app.html` | Suíte 11 |
| 2 | `phoneMask` | `web/static/js/app.js`, `auth/cadastro.html`, `app/perfil.html` | Suíte 1, 5 |
| 3 | `passwordFields` | `web/static/js/app.js`, `partials/password-fields.html` | Suíte 1, 4 |
| 4 | `collectivePicker` | `web/static/js/app.js`, `auth/cadastro.html`, `app/coletivos.html` | Suíte 1, 6 |
| 5 | `datePicker` | `web/static/js/app.js`, `partials/event-fields.html` | Suíte 8, 11 |
| 6 | `timeSpinner` | `web/static/js/app.js`, `partials/event-fields.html` | Suíte 8 |
| 7 | `durationSpinner` | `web/static/js/app.js`, `partials/event-fields.html` | Suíte 8 |
| 8 | `moneyInput` | `web/static/js/app.js`, `partials/event-fields.html` | Suíte 8 |
| 9 | `eventDynamic` | `web/static/js/app.js`, `app/evento-form.html` | Suíte 8, 11 |
| 10 | `calendarPage` | `web/static/js/app.js`, `app/home.html` | Suíte 9, 11 |

### 2.3 Templates de E-mail Transacional (10 Templates)

| # | Template (`internal/mail/render.go`) | Assunto Esperado | Gatilho | Suíte |
|---|---|---|---|---|
| 1 | `verify_email` | `Confirme seu e-mail · Agenda Clubber` | Cadastro novo ou reenvio de confirmação | Suíte 1, 2, 10 |
| 2 | `existing_account` | `Você já tem uma conta · Agenda Clubber` | Cadastro ou troca de e-mail para endereço já ocupado | Suíte 1, 5, 10 |
| 3 | `admin_new_signup` | `Novo cadastro para aprovar: <Nome>` | Confirmação de e-mail concluída (`POST /confirmar`) | Suíte 2, 10 |
| 4 | `password_reset` | `Redefinir sua senha · Agenda Clubber` | `POST /senha/esqueci` para conta ativa | Suíte 4, 10 |
| 5 | `decision` | `Atualização do seu cadastro na Agenda Clubber` | Decisões do Admin ou Administrador de coletivo | Suíte 6, 7, 10 |
| 6 | `owner_join_request` | `Novo pedido para entrar em <Coletivo>` | Pedido de entrada visível aos Administradores do coletivo | Suíte 6, 7, 10 |
| 7 | `admin_request` | `Pedido de entrada: ...` / `Novo coletivo ou produtora para aprovar: ...` / `Nome alterado: ... → ...` | `POST /coletivos/entrar`, `POST /coletivos/novo`, `POST /coletivo/:id/nome` | Suíte 6, 10 |
| 8 | `change_email` | `Confirme seu novo e-mail · Agenda Clubber` | `POST /perfil/email` (enviado ao novo endereço) | Suíte 5, 10 |
| 9 | `email_change_notice` | `Troca de e-mail pendente · Agenda Clubber` | `POST /perfil/email` (enviado ao endereço atual) | Suíte 5, 10 |
| 10 | `password_expiring` | `Sua senha vence amanhã · Agenda Clubber` ou `Sua senha vence em N dias · Agenda Clubber` | Job diário `SendPasswordReminders` (janela `<= 7` dias) | Suíte 4, 10 |

### 2.4 Itens de SecOps (PR #7 — 10 Itens)

| Item | Documento / Regra | Verificação no UAT | Suíte |
|---|---|---|---|
| `ACL-21` | `ADR-001` (`$inc: {rev: 1}` em `mustExist` dentro de `inTx`) | Integridade referencial sem órfãos em exclusão de conta/coletivo concorrente com escrita | Suíte 7, 10 |
| `ACL-32` | `go-mail v0.8.2` (fechamento de socket em erro de handshake SMTP) | Envio contínuo no Mailpit sem vazamento de conexões + conferência de `go.mod` | Suíte 0, 10 |
| `ACL-37` | `Outbox.Claim` ignora `limit < 1` ou `lease <= 0` | Worker drena outbox normalmente sem loops de lease inválido (evidência de código/teste + delivery) | Suíte 10 |
| `ACL-38` | `SMTPSender.Send` ignora erro de `QUIT` após `250 DATA` | Entrega única sem reenvio duplicado no Mailpit (sem spam de retentativa) | Suíte 10 |
| `ACL-39` | `BDR-001` (risco aceito: enumeração em 2 etapas no cadastro) | Mitigações ativas: rate limit 5/h por IP + expurgo automático de não confirmados em 7 dias | Suíte 1, 10 |
| `ACL-41` | `ADR-002` (limiters isolados IP vs e-mail + `ValidateSignup` antes de `SignupEmail`) | 5 POSTs inválidos não consomem cota do e-mail; cota IP e cota e-mail são independentes | Suíte 1, 5 |
| `ACL-43` | `BDR-001` (lockout compartilhado 5 tentativas / 15 min) | 5 erros combinados entre `/entrar`, `/senha/trocar` e `/senha/verificar` bloqueiam a conta por 15 min | Suíte 4 |
| `ACL-48` | `BDR-001` (saudação sem nome `"Olá!"` em `verify_email`) | E-mail `verify_email` (cadastro e reenvio) não contém nome submetido; `existing_account` mantém nome salvo | Suíte 1, 2, 10 |
| `ACL-49` | Upgrade `golang.org/x/crypto v0.59.0` (`GO-2026-5932`) | Conferência de versão em `go.mod` + fluxo Argon2id operacional dentro do limiar `<= 1500 ms` | Suíte 0, 3 |
| `ACL-67` | `domain.TextProblem` rejeita Unicode `Cf` e `U+2028`/`U+2029` | Inputs com ZWSP (`\u200B`), Bidi RLO (`\u202E`) ou `\u2028` são rejeitados com erro de validação | Suíte 1, 5, 8 |

---

## 3. Suítes Determinísticas de UAT (`Suíte 0` a `Suíte 11`)

---

### Suíte 0 — Infraestrutura, Saúde, Estáticos, Cabeçalhos de Segurança, CSP, Origem/CSRF e Identidade Visual

- **Escopo:** Liveness (`/healthz`), allowlist de estáticos (`/static/*filepath` GET/HEAD), cabeçalhos de segurança e CSP, proteção `sameOrigin` (Origin + `Sec-Fetch-Site`), limite de corpo de 64 KiB (`413`), ausência de redirect em trailing slash (`404`), dependências SecOps (`ACL-32`, `ACL-49`) e identidade visual CircuitoNE.
- **Persona:** Anônimo (via `curl` e navegador em `1280×800`).
- **Pré-condições:** Stack UAT ativa em `http://localhost:8120`; Mailpit em `http://127.0.0.1:8025`.
- **BDRs / ADRs:** `@BDR-001`, `@BDR-007`, `@ADR-001`, `@ADR-002`.
- **Cenários:** `@BDD-S0-Infra`, `@BDD-S0-SecHeaders`, `@BDD-S0-CSRF`, `@BDD-S0-BodyLimit`, `@BDD-S0-Visual`.
- **Rotas:** `GET /healthz`, `GET /static/*filepath`, `HEAD /static/*filepath`.
- **Componentes:** Nenhum (verificação de carga de `app.js`, `htmx`, `alpine-csp` e CSS tokens).

| ID | Tipo | Persona / Viewport | Passos Exatos | Resultado Observável Esperado |
|---|---|---|---|---|
| `UAT-S0-01` | Esperado | Anônimo / CLI | Executar `curl -i http://localhost:8120/healthz` e `agenda healthcheck`. | HTTP `200 OK`, corpo exato `ok`, cabeçalho `Cache-Control: no-store`, e `agenda healthcheck` encerra com código `0`. Latência `<= 300 ms`. |
| `UAT-S0-02` | Segurança | Anônimo / CLI | Inspecionar os cabeçalhos HTTP de `GET /healthz` e `GET /entrar`. | Todos presentes exatamente: `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, `Permissions-Policy: camera=(), microphone=(), geolocation=(), payment=()`, `Cross-Origin-Opener-Policy: same-origin`. |
| `UAT-S0-03` | Esperado | Anônimo / CLI | Obter a URL versionada de um CSS em `GET /entrar` (ex.: `/static/css/tokens.css?v=<hash10>`). Fazer `GET`, `HEAD` e `GET` com `If-None-Match: "<hash10>"`, e também `GET /static/css/tokens.css` (sem `?v=`). | Com `?v=<hash10>`: `200`, `Content-Type: text/css; charset=utf-8`, `ETag: "<hash10>"`, `Cache-Control: public, max-age=31536000, immutable`. `HEAD` retorna `200` sem corpo. Com `If-None-Match`: `304 Not Modified`. Sem `?v=`: `Cache-Control: no-cache`. |
| `UAT-S0-04` | Segurança | Anônimo / CLI | Solicitar `GET /static/../../go.mod`, `GET /static/.hidden`, `GET /static/nao-existe.js`, `GET /healthz/` e `GET /entrar/`. | Todos retornam `404 Not Found` (nunca `301`/`302` por trailing slash — `RedirectTrailingSlash=false`) mantendo todos os cabeçalhos de segurança de `UAT-S0-02`. |
| `UAT-S0-05` | Segurança | Anônimo / CLI | Enviar `POST /entrar` sem cabeçalho `Origin`; depois com `Origin: https://evil.example`; depois com `Origin: http://localhost:8120` + `Sec-Fetch-Site: cross-site`. | Todas as 3 requisições retornam `403 Forbidden` com corpo `origem não permitida`, antes de qualquer consulta ao banco ou sessão. |
| `UAT-S0-06` | Segurança | Anônimo / CLI | Enviar `POST /entrar` com `Origin: http://localhost:8120` e payload de `65537` bytes (`> 64 KiB`). | HTTP `413 Request Entity Too Large` com corpo `corpo grande demais` e cabeçalhos de segurança intactos. |
| `UAT-S0-07` | UX/A11y | Anônimo / `1280×800` | Abrir `http://localhost:8120/entrar` no navegador e inspecionar DevTools (Console, Network, Styles). | Tema escuro CircuitoNE (`#050506`, accent `#ff2040`, fontes `JetBrains Mono` e `Space Mono` locais sem CDN), cantos retos, wordmark `◢◤ AGENDA_CLUBBER`, título terminando em `· AGENDA CLUBBER`, rodapé `$ feito_por --dev=IgnisDev` e `mailto:ignisdev@magalz.space`, `0` erros JS, `0` violações CSP, `0` assets quebrados. |
| `UAT-S0-08` | Segurança | CLI | Verificar `go.mod` para dependências SecOps (`ACL-49`, `ACL-32`). | `golang.org/x/crypto` em versão `>= v0.59.0` e `github.com/wneessen/go-mail` em versão `>= v0.8.2`. |

---

### Suíte 1 — Cadastro Público, Widgets de Cadastro, Anti-Enumeração e SecOps (`ACL-39`, `ACL-41`, `ACL-48`, `ACL-67`)

- **Escopo:** Fluxo completo de `/cadastro`, sugestões `/coletivos/sugestoes`, página `/cadastro/enviado`, componentes `phoneMask`, `passwordFields` e `collectivePicker`, anti-enumeração de e-mail, pré-validação `ValidateSignup` + isolamento de limiters (`ACL-41`), rejeição Unicode `Cf` (`ACL-67`), saudação sem nome (`ACL-48`) e mitigações de `ACL-39`.
- **Persona:** Anônimo (criando contas `uat-*@agenda.test`).
- **Pré-condições:** `Coletivo Alfa` (aprovado, desbloqueado) e `Coletivo Delta` (aprovado, `EventsBlocked=true`) existem no banco; Mailpit limpo.
- **BDRs / ADRs:** `@BDR-001` (`ACL-39`, `ACL-48`), `@BDR-002`, `@ADR-002` (`ACL-41`).
- **Cenários:** `@BDD-S1-SignupNew`, `@BDD-S1-SignupJoin`, `@BDD-S1-AntiEnum`, `@BDD-S1-RateLimit`, `@BDD-S1-UnicodeCf`.
- **Rotas:** `GET /cadastro`, `POST /cadastro`, `GET /cadastro/enviado`, `GET /coletivos/sugestoes`.
- **Componentes:** `phoneMask`, `passwordFields`, `collectivePicker`.

| ID | Tipo | Persona / Viewport | Passos Exatos | Resultado Observável Esperado |
|---|---|---|---|---|
| `UAT-S1-01` | Esperado | Anônimo / `1280×800` | Abrir `GET /cadastro`. No campo telefone (`#phone`), digitar `85988887777` e testar colar `+55 85 98888-7777`. Na senha (`#password`), digitar gradualmente até cumprir todos os 7 requisitos e preencher `#password_confirm`. | `phoneMask` formata `(85) 98888-7777` e remove prefixo `+55` ao colar. `passwordFields` marca cada item `li[data-rule]` com `.is-ok` e texto `.sr-only` ` (atendido)` (incluindo `not_email` reagindo em tempo real ao `#email`), exibindo `as senhas conferem` quando iguais e `as senhas não conferem` quando diferentes. |
| `UAT-S1-02` | Esperado | Anônimo / `1280×800` | Em `GET /cadastro`, selecionar tipo `coletivo`, digitar `Alf` em `#collective_name` e aguardar `>= 250 ms`. Navegar nas sugestões com `ArrowDown`/`Enter`. | Requisição `GET /coletivos/sugestoes?q=Alf&tipo=coletivo` retorna `200` com `Coletivo Alfa`. Ao selecionar, `#collective_id` é preenchido, `#collective_name` recebe `Coletivo Alfa` e ambos os papéis (`Membro` e `Administrador`) ficam habilitados. |
| `UAT-S1-03` | Esperado | Anônimo / `1280×800` | Preencher cadastro completo para `uat-novo1@agenda.test` escolhendo `Coletivo Alfa` como `member` (`role=member`) e submeter `POST /cadastro`. | Redirecionamento `303` (ou `204` + `HX-Redirect`) para `/cadastro/enviado` (`200`). E-mail `verify_email` chega no Mailpit para `uat-novo1@agenda.test` com assunto `Confirme seu e-mail · Agenda Clubber` e saudação exata `Olá!` **sem** o nome digitado (`ACL-48`). Latência POST `<= 1500 ms`. |
| `UAT-S1-04` | Esperado | Anônimo / `1280×800` | Em `GET /cadastro`, digitar um nome de coletivo inédito `Coletivo UAT Aurora` (sem selecionar sugestão) para `uat-novo2@agenda.test`. Observar o comportamento do papel e submeter. | `collectivePicker` limpa `#collective_id`, seleciona `Administrador` (`role=owner`), desabilita `Membro` e exibe dica `nome novo: quem cria um coletivo novo entra como administrador (master)...`. Submissão redireciona para `/cadastro/enviado` e cria usuário `pending_email` + coletivo `pending`. |
| `UAT-S1-05` | Borda | Anônimo / CLI | Enviar `POST /cadastro` forçando `collective_id=""`, `collective_name="Coletivo UAT Invalido"` e `role=member` (sem JS). | HTTP `422 Unprocessable Entity` com erro no campo `role`: `quem cria um coletivo novo entra como administrador (master)`. Senhas não são reimpressas no HTML retornado. |
| `UAT-S1-06` | Segurança | Anônimo / `1280×800` | Submeter `POST /cadastro` com e-mail já cadastrado (`dono@agenda.test`), dados válidos e `Coletivo Alfa` (`role=member`). | Resposta idêntica ao cadastro novo: redireciona para `/cadastro/enviado` (anti-enumeração). No Mailpit, `dono@agenda.test` recebe `existing_account` (`Você já tem uma conta · Agenda Clubber`) contendo o nome salvo (`Administrador Dev`) e links para `/entrar` e `/senha/esqueci`, sem gerar token de reset. |
| `UAT-S1-07` | Segurança | Anônimo / CLI | (`ACL-67`) Enviar `POST /cadastro` com caractere Unicode `Cf` no nome (ex.: `Maria\u200BSilva`, `Maria\u202ESilva`) ou separador `U+2028` no nome do coletivo. | HTTP `422 Unprocessable Entity` com erro `não use quebras de linha nem caracteres de controle` no respectivo campo; nenhum registro criado no banco. |
| `UAT-S1-08` | Segurança | Anônimo / CLI | (`ACL-41` / `ADR-002`) Para `uat-rl1@agenda.test`, enviar 4 submissões `POST /cadastro` **inválidas** (ex.: senha vazia), e em seguida 1 submissão válida (com novo IP ou antes de estourar cota de IP). Depois estourar a cota de IP (5 POSTs/hora) e confirmar 429. | Submissões inválidas retornam `422` e **não** consomem `Limits.SignupEmail` (`ValidateSignup` roda antes). A submissão válida é aceita (`303`). Ao atingir o 6º POST/hora do mesmo IP, retorna `429 Too Many Requests` com `muitas tentativas. espere alguns minutos e tente de novo.` (`ACL-39` mitigado). |
| `UAT-S1-09` | Borda | Anônimo / CLI | Consultar `GET /coletivos/sugestoes?q=a&tipo=coletivo` (1 caractere), `GET /coletivos/sugestoes?q=Alfa&tipo=invalido`, e enviar 61 consultas/minuto. | `q` com `< 2` caracteres retorna `200` com fragmento vazio; `tipo=invalido` retorna `400 requisição inválida`; 61ª consulta no minuto retorna `429` sem corpo. Coletivos congelados (`Locked=true`) não aparecem nas sugestões. |

---

### Suíte 2 — Confirmação de E-mail, Reenvio e Espera de Aprovação

- **Escopo:** Confirmação em 2 passos (`GET /confirmar` não consome token; `POST /confirmar` consome), reenvio anti-enumeração (`POST /confirmar/reenviar`), página `/aguardando`, disparo de `admin_new_signup` para o Admin e validação `ACL-48` no reenvio.
- **Persona:** `pendente-email@agenda.test`, `uat-novo1@agenda.test` (criado na Suíte 1) e Anônimo.
- **Pré-condições:** Link de confirmação de `uat-novo1@agenda.test` disponível no Mailpit; link de `pendente-email@agenda.test` do `seed-dev`.
- **BDRs / ADRs:** `@BDR-001` (`ACL-48`), `@BDR-002`, `@ADR-002` (`ACL-41`).
- **Cenários:** `@BDD-S2-GetSafe`, `@BDD-S2-PostConfirm`, `@BDD-S2-Resend`, `@BDD-S2-Expired`.
- **Rotas:** `GET /confirmar`, `POST /confirmar`, `POST /confirmar/reenviar`, `GET /aguardando`.
- **Componentes:** Nenhum (formulários server-rendered puros).

| ID | Tipo | Persona / Viewport | Passos Exatos | Resultado Observável Esperado |
|---|---|---|---|---|
| `UAT-S2-01` | Segurança | Anônimo / `1280×800` | Abrir `GET /confirmar?token=<token-valido-uat-novo1>` duas vezes seguidas (simulando pré-visualização de scanner de e-mail) sem clicar no botão. | Ambas retornam `200 OK` exibindo apenas o botão de confirmação (`confirmStateButton`); o token **não** é consumido e a conta permanece `pending_email`. |
| `UAT-S2-02` | Esperado | Anônimo / `1280×800` | Na tela de `UAT-S2-01`, clicar no botão para enviar `POST /confirmar`. | Redirecionamento `303` para `GET /aguardando` (`200`, título `Cadastro em análise · AGENDA CLUBBER`). Conta passa para `pending_approval`. No Mailpit, `admin@agenda.test` recebe `admin_new_signup` (`Novo cadastro para aprovar: <Nome>`). |
| `UAT-S2-03` | Borda | Anônimo / `1280×800` | Reenviar `POST /confirmar` com o mesmo token já utilizado em `UAT-S2-02`, e também acessar `GET /confirmar` sem query `token`. | Ambos retornam `400 Bad Request` exibindo formulário de reenvio (`confirmStateResend`) e mensagem `este link de confirmação expirou ou já foi usado.` |
| `UAT-S2-04` | Segurança | Anônimo / `1280×800` | Em `POST /confirmar/reenviar`, submeter primeiro `pendente-email@agenda.test` (conta pendente) e depois `inexistente@agenda.test` (não cadastrado) e `dono@agenda.test` (já ativo). | Todas as 3 respostas retornam `200 OK` com a mesma mensagem `se houver um cadastro aguardando confirmação para este e-mail, enviamos um novo link. ele vale por 24 horas.` No Mailpit, **apenas** `pendente-email@agenda.test` recebe novo `verify_email` com saudação `Olá!` (sem nome, `ACL-48`); o token anterior de `pendente-email` é invalidado. |
| `UAT-S2-05` | Segurança | Anônimo / CLI | Enviar `POST /confirmar/reenviar` 6 vezes em 1 hora para o mesmo e-mail/IP; enviar `POST /confirmar` 11 vezes em 1 minuto com tokens inválidos. | 6º reenvio retorna `429` com `msgTooMany`; 11º `POST /confirmar` retorna `429` mantendo o token no campo oculto para retentativa posterior. |

---

### Suíte 3 — Autenticação, Sessões, Logout e Estados de Conta

- **Escopo:** Login (`GET/POST /entrar`), logout (`POST /sair`), todos os 5 estados de conta no login (`active`, `pending_email`, `pending_approval`, `disabled`, `locked`/credencial inválida), proteção `safeNext` contra open redirect, cookie `__Host-acl_session`, expiração de sessão (7d idle / 30d max), resposta `401` + `HX-Redirect` em requisições HTMX sem sessão.
- **Persona:** `dono@agenda.test`, `pendente-email@agenda.test`, `pendente-aprovacao@agenda.test`, conta desativada, Anônimo.
- **Pré-condições:** Contas do `seed-dev` ativas; uma conta `uat-disabled@agenda.test` bloqueada (ou bloqueada via `/admin/contas` na Suíte 7).
- **BDRs / ADRs:** `@BDR-001`, `@BDR-002`.
- **Cenários:** `@BDD-S3-LoginStates`, `@BDD-S3-SafeNext`, `@BDD-S3-SessionCookie`, `@BDD-S3-HTMX401`.
- **Rotas:** `GET /entrar`, `POST /entrar`, `POST /sair`.
- **Componentes:** Nenhum.

| ID | Tipo | Persona / Viewport | Passos Exatos | Resultado Observável Esperado |
|---|---|---|---|---|
| `UAT-S3-01` | Esperado | `dono@agenda.test` / `1280×800` | Acessar `GET /` sem sessão; verificar redirecionamento para `/entrar`; preencher e-mail e `SEED_PASSWORD` e enviar `POST /entrar`. | Redireciona `303` para `/`. Cookie `__Host-acl_session` definido com `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, `Max-Age=2592000` (30d; servidor aplica 7d idle / 30d máximo absoluto). Latência Argon2 `<= 1500 ms`. |
| `UAT-S3-02` | Esperado | Anônimo / `1280×800` | Testar login com senha correta para: (a) `pendente-email@agenda.test`; (b) `pendente-aprovacao@agenda.test`; (c) conta `disabled`. Testar também senha incorreta para `dono@agenda.test` e e-mail inexistente `naoexiste@agenda.test`. | Todos retornam `401 Unauthorized` (e-mail preservado, senha limpa): (a) `confirme seu e-mail antes de entrar.` + formulário de reenvio; (b) `seu cadastro aguarda a aprovação do admin. você recebe um e-mail quando for aprovado.`; (c) `esta conta está desativada. fale com` + link de contato; senha errada ou e-mail inexistente exibem a mesma mensagem `e-mail ou senha incorretos. depois de 5 tentativas erradas, a conta fica bloqueada por 15 minutos.` |
| `UAT-S3-03` | Segurança | `dono@agenda.test` / CLI | Testar `GET /entrar?next=https://evil.com`, `?next=//evil.com`, `?next=/\evil.com`, `?next=/coletivos` e fazer login. | Apenas `next=/coletivos` redireciona para `/coletivos`; todas as URLs externas, protocol-relative ou com barra invertida são descartadas por `safeNext`, redirecionando para `/`. |
| `UAT-S3-04` | Esperado | `dono@agenda.test` / `1280×800` | Estando logado, clicar em `[→] sair` (`POST /sair`). Em seguida tentar reutilizar o cookie antigo em `GET /perfil`. | `POST /sair` redireciona `303` para `/entrar` e envia `Set-Cookie: __Host-acl_session=; Max-Age=0`. Reuso do cookie antigo é rejeitado (sessão removida do MongoDB) e redireciona para `/entrar?next=%2Fperfil`. |
| `UAT-S3-05` | Segurança | Anônimo / CLI | Enviar `GET /calendario` com cabeçalho `HX-Request: true` sem cookie de sessão. | HTTP `401 Unauthorized` com cabeçalho `HX-Redirect: /entrar` (nunca `303`, evitando que o HTMX faça swap da tela de login dentro do slot). |
| `UAT-S3-06` | Segurança | Anônimo / CLI | Enviar 11 requisições `POST /entrar` em 1 minuto do mesmo IP. | A 11ª retorna `429 Too Many Requests` com `msgTooMany` antes de consultar o banco. |

---

### Suíte 4 — Gestão de Senha, Redefinição, Expiração e Lockout Compartilhado (`ACL-43`)

- **Escopo:** Recuperação (`GET/POST /senha/esqueci`, `GET/POST /senha/redefinir`), troca autenticada (`GET/POST /senha/trocar`), verificação ao vivo de histórico (`POST /senha/verificar`), lockout compartilhado 5 tentativas / 15 min (`ACL-43`), histórico das 2 últimas senhas (`auth.HistorySize = 2`), expiração de 90 dias (`senha-vencida@agenda.test`) e aviso de 7 dias (`senha-quase@agenda.test`).
- **Persona:** `senha-vencida@agenda.test`, `senha-quase@agenda.test`, `uat-lockout@agenda.test`.
- **Pré-condições:** Contas do `seed-dev` prontas; criar e aprovar `uat-lockout@agenda.test` para testes de lockout sem afetar contas de seed.
- **BDRs / ADRs:** `@BDR-001` (`ACL-43`), `@BDR-002`, `@ADR-002` (`ACL-41`).
- **Cenários:** `@BDD-S4-ForgotReset`, `@BDD-S4-ExpiredGate`, `@BDD-S4-HistoryCheck`, `@BDD-S4-SharedLockout`.
- **Rotas:** `GET /senha/esqueci`, `POST /senha/esqueci`, `GET /senha/redefinir`, `POST /senha/redefinir`, `GET /senha/trocar`, `POST /senha/trocar`, `POST /senha/verificar`.
- **Componentes:** `passwordFields` + HTMX em `POST /senha/verificar`.

| ID | Tipo | Persona / Viewport | Passos Exatos | Resultado Observável Esperado |
|---|---|---|---|---|
| `UAT-S4-01` | Esperado | Anônimo / `1280×800` | Em `GET /senha/esqueci`, submeter `uat-lockout@agenda.test` (ativa) e depois `inexistente@agenda.test`. | Ambas retornam `200` com `se houver uma conta ativa com este e-mail, enviamos um link para criar uma nova senha. ele vale por 1 hora.` Apenas `uat-lockout@agenda.test` recebe `password_reset` no Mailpit. |
| `UAT-S4-02` | Esperado | Anônimo / `1280×800` | Abrir o link `GET /senha/redefinir?token=...` (validar que o GET não consome o token). Submeter nova senha válida via `POST /senha/redefinir`. Tentar reutilizar uma sessão antiga da conta e o mesmo token novamente. | `POST` retorna `200` com `senha alterada. entre com a nova senha.`, limpa cookie de sessão e revoga **todas** as sessões anteriores da conta. Reuso do token retorna `400` (`este link expirou ou já foi usado.`). |
| `UAT-S4-03` | Esperado | `senha-vencida@agenda.test` / `1280×800` | Fazer login com `senha-vencida@agenda.test`. Tentar navegar para `GET /`, `GET /perfil` e `GET /calendario` (com `HX-Request: true`). | `requireFreshPassword` bloqueia todas as rotas exceto `/senha/trocar`, `/senha/verificar` e `/sair`: rotas normais redirecionam `303` para `/senha/trocar`; HTMX recebe `401` + `HX-Redirect: /senha/trocar`. |
| `UAT-S4-04` | Esperado | `senha-vencida@agenda.test` / `1280×800` | Em `GET /senha/trocar`, informar `current_password = SEED_PASSWORD` e testar no campo `#password` a senha anterior `Anterior-<SEED_PASSWORD>` (histórico de 2 senhas). Observar resposta de `POST /senha/verificar` e tentar submeter. Em seguida, trocar para uma senha inédita válida com 2 sessões abertas. | `POST /senha/verificar` mantém item de histórico como pendente (`pending`) para a senha anterior e marca `ok` para senha inédita. Submeter senha no histórico retorna `422`. Submeter senha inédita retorna `200` (`senha alterada. as outras sessões foram encerradas.`), mantém a sessão atual ativa, encerra a segunda sessão e libera acesso ao calendário `/`. |
| `UAT-S4-05` | Segurança | `uat-lockout@agenda.test` / CLI | (`ACL-43` / `BDR-001`) Estando autenticado com `uat-lockout@agenda.test`, enviar: 2 erros de `current_password` em `POST /senha/trocar`, 2 erros em `POST /senha/verificar`, e 1 erro em `POST /entrar` (total = 5 tentativas). Em seguida, tentar `POST /senha/trocar` com a senha correta e `POST /entrar` com a senha correta. | Na 5ª falha, a conta entra em lockout por 15 minutos (`LockedUntil`). Enquanto bloqueada: `POST /senha/trocar` retorna `429` (`muitas tentativas de senha incorreta. a conta está bloqueada por 15 minutos.`), `POST /senha/verificar` retorna `pending` sem rodar Argon2, e `POST /entrar` retorna `401` com `loginMsgRejected` mesmo com a senha correta. |
| `UAT-S4-06` | Segurança | `uat-lockout@agenda.test` / CLI | (`ACL-43`) Em conta não bloqueada, enviar `POST /senha/trocar` com `current_password=""` ou erro de campo em `password`, e `POST /senha/verificar` com `password` vazio ou `> 128` caracteres. Depois errar 2 vezes e acertar 1 vez (mesmo com nova senha no histórico). | Erros de campo / inputs vazios ou longos **não** incrementam `FailedLogins`. Um `current_password` válido reseta `FailedLogins` para `0` mesmo que a nova senha seja recusada pelo histórico. |
| `UAT-S4-07` | Esperado | `senha-quase@agenda.test` / `1280×800` | Fazer login com `senha-quase@agenda.test` (87 dias) e abrir `GET /`. Conferir também o Mailpit após o `dailyLoop` no startup. | Banner `.expiry-banner` exibido no topo do `<main>` com `sua senha vence em 3 dias.` e link `[→] trocar agora` (`/senha/trocar`); em `/senha/trocar`, o banner não se repete. No Mailpit, existe exatamente 1 e-mail `password_expiring` (`Sua senha vence em 3 dias · Agenda Clubber`), sem duplicata ao reiniciar `serve`. |

---

### Suíte 5 — Perfil e Troca de E-mail em 2 Etapas

- **Escopo:** Edição de nome/telefone/WhatsApp (`GET/POST /perfil`), solicitação de troca de e-mail (`POST /perfil/email`), confirmação pública em 2 etapas (`GET/POST /confirmar-email`), disparo simultâneo de `change_email` + `email_change_notice`, anti-enumeração com `existing_account`, sobrevivência de sessão e todos os 5 códigos de `perfilNotices`.
- **Persona:** `uat-perfil@agenda.test` (conta ativa dedicada).
- **Pré-condições:** Conta `uat-perfil@agenda.test` ativa e autenticada; `dono@agenda.test` já existe.
- **BDRs / ADRs:** `@BDR-003`, `@ADR-002` (`ACL-41`).
- **Cenários:** `@BDD-S5-UpdateProfile`, `@BDD-S5-EmailChangeFlow`, `@BDD-S5-EmailTakenAntiEnum`, `@BDD-S5-SessionSurvives`.
- **Rotas:** `GET /perfil`, `POST /perfil`, `POST /perfil/email`, `GET /confirmar-email`, `POST /confirmar-email`.
- **Componentes:** `phoneMask`.

| ID | Tipo | Persona / Viewport | Passos Exatos | Resultado Observável Esperado |
|---|---|---|---|---|
| `UAT-S5-01` | Esperado | `uat-perfil@agenda.test` / `1280×800` | Abrir `GET /perfil`, alterar `name`, `phone` (validando máscara `phoneMask`) e checkbox `phone_is_whatsapp`, e enviar `POST /perfil`. | Redireciona `303` para `/perfil?msg=perfil-salvo`, exibindo `.status--ok` `perfil salvo.` e valores atualizados. Nenhuma entrada gravada em `/admin/historico`. |
| `UAT-S5-02` | Borda | `uat-perfil@agenda.test` / `1280×800` | Em `POST /perfil`, enviar `name` com caractere `Cf` (`\u200B`, `ACL-67`) ou telefone com 8 dígitos. Em `POST /perfil/email`, enviar o próprio e-mail atual (`uat-perfil@agenda.test`). | Ambos retornam `422`: erro de controle/comprimento no perfil; no e-mail próprio, erro de campo exato `este já é o seu e-mail`. |
| `UAT-S5-03` | Segurança | `uat-perfil@agenda.test` / `1280×800` | Em `POST /perfil/email`, solicitar troca para `dono@agenda.test` (e-mail já em uso por outra conta). | Redireciona `303` para `/perfil?msg=email-pendente` (`.status--ok`: `se o e-mail estiver disponível, enviamos um link de confirmação. ele vale por 24 horas.`). Nenhum token `change_email` é criado; `dono@agenda.test` recebe `existing_account` no Mailpit. |
| `UAT-S5-04` | Esperado | `uat-perfil@agenda.test` / `1280×800` | Em `POST /perfil/email`, solicitar troca para `uat-perfil-novo@agenda.test`. Conferir Mailpit. Acessar `GET /confirmar-email?token=...` (confirmar que GET só mostra o botão e não altera o e-mail). Clicar para enviar `POST /confirmar-email`. | No pedido: redireciona para `/perfil?msg=email-pendente`; Mailpit recebe `change_email` para `uat-perfil-novo@agenda.test` e `email_change_notice` para `uat-perfil@agenda.test`. Após `POST /confirmar-email`: redireciona para `/perfil?msg=email-confirmado` (`.status--ok`: `e-mail confirmado. seu endereço foi atualizado.`), **sessão continua autenticada** sem exigir novo login e nenhum segundo e-mail é enviado. |
| `UAT-S5-05` | Borda | `uat-perfil-novo@agenda.test` / `1280×800` | Reenviar `POST /confirmar-email` com o token já gasto; testar também `GET /perfil?msg=email-em-uso` e `GET /perfil?msg=codigo-desconhecido`. | Token gasto redireciona para `/perfil?msg=email-invalido` (`.status--error`: `este link expirou ou já foi usado.`). `email-em-uso` renderiza `.status--error` (`este e-mail já está em uso por outra conta. peça a troca novamente.`). Código desconhecido não exibe nenhum texto nem ecoa o valor. |
| `UAT-S5-06` | Segurança | `uat-perfil-novo@agenda.test` / CLI | Solicitar 2 vezes seguidas troca de e-mail para endereços novos e tentar usar o 1º token; testar também rate limit `EmailAction` / `EmailActionEmail` (6º pedido na hora). | O 2º pedido aposenta o 1º token (1º token passa a redirecionar para `email-invalido`). 6º pedido retorna `429` com `msgTooMany`. |

---

### Suíte 6 — Coletivos, Papéis e Gestão do Administrador (`/coletivos`, `/coletivo/:id`)

- **Escopo:** Página `Meus Coletivos` (`GET /coletivos`), pedidos de entrada (`POST /coletivos/entrar`) e criação (`POST /coletivos/novo`), `collectivePicker` em `data-mode="join"`, página de gestão do coletivo (`GET /coletivo/:id`) e suas 5 ações (`pedido/aprovar`, `pedido/recusar`, `membro/promover`, `membro/rebaixar`, `nome`), ocultação `404` para quem não é Administrador aprovado (Membro, Admin do site, estranho, coletivo pendente), imutabilidade do `Master`, guarda do último Administrador, autorrebaixamento (`?msg=voce-rebaixado`) e disparo de `owner_join_request` e `admin_request`.
- **Persona:** `dono@agenda.test` (Master de `Coletivo Alfa` e `Produtora Beta`), `membro@agenda.test`, `pedido@agenda.test`, `admin@agenda.test`.
- **Pré-condições:** `Coletivo Alfa` (dono=Master, membro=Membro), `Produtora Beta` (dono=Master, `pedido@agenda.test` com pedido pendente).
- **BDRs / ADRs:** `@BDR-004`, `@BDR-006`, `@ADR-001` (`ACL-21`).
- **Cenários:** `@BDD-S6-JoinAndNew`, `@BDD-S6-Hide404`, `@BDD-S6-PromoteDemote`, `@BDD-S6-MasterGuard`, `@BDD-S6-Rename`.
- **Rotas:** `GET /coletivos`, `POST /coletivos/entrar`, `POST /coletivos/novo`, `GET /coletivo/:id`, `POST /coletivo/:id/pedido/aprovar`, `POST /coletivo/:id/pedido/recusar`, `POST /coletivo/:id/membro/promover`, `POST /coletivo/:id/membro/rebaixar`, `POST /coletivo/:id/nome`.
- **Componentes:** `collectivePicker` (`data-mode="join"`).

| ID | Tipo | Persona / Viewport | Passos Exatos | Resultado Observável Esperado |
|---|---|---|---|---|
| `UAT-S6-01` | Esperado | `membro@agenda.test` / `1280×800` | Abrir `GET /coletivos`. No formulário de entrar em coletivo (`data-mode="join"`), digitar `Beta` no picker, selecionar `Produtora Beta`, escolher `Membro` e submeter `POST /coletivos/entrar`. Em seguida submeter `POST /coletivos/novo` com `name="Produtora UAT Sul"` e `kind="produtora"`. | Primeiro POST redireciona para `/coletivos?msg=pedido-enviado` (`pedido enviado. você recebe um e-mail quando ele for decidido.`); Mailpit recebe `admin_request` (`Kind="join"`) para o Admin e `owner_join_request` para `dono@agenda.test`. Segundo POST redireciona para `/coletivos?msg=coletivo-pedido` (`pedido de coletivo novo enviado ao admin.`) e envia `admin_request` (`Kind="new_collective"`) ao Admin. |
| `UAT-S6-02` | Borda | `membro@agenda.test` / CLI | Enviar `POST /coletivos/entrar` com `collective_id=""` e `collective_name="Nome Que Nao Existe"`. Depois tentar entrar em um coletivo congelado (`Locked=true`). | Nome inexistente retorna `422` com erro `escolha um coletivo da lista` (nunca cria coletivo novo). Coletivo congelado retorna `409 Conflict` com alerta `este coletivo não está aceitando pedidos agora.` |
| `UAT-S6-03` | Segurança | `membro@agenda.test`, `admin@agenda.test`, `delta@agenda.test` / CLI | Acessar `GET /coletivo/<id-alfa>` e enviar `POST /coletivo/<id-alfa>/nome` (e `/membro/promover`) logado como: (a) `membro@agenda.test` (Membro de Alfa); (b) `admin@agenda.test` (Admin do site, não Administrador de Alfa); (c) `delta@agenda.test` (estranho); (d) `GET /coletivo/<id-gama>` (coletivo `pending`). | Todos os 4 casos retornam **`404 Not Found`** idêntico a rota inexistente (`answerNotFound`), **nunca** `403`, sem revelar existência do coletivo. |
| `UAT-S6-04` | Esperado | `dono@agenda.test` / `1280×800` | Abrir `GET /coletivo/<id-beta>` como `dono@agenda.test`. Aprovar o pedido de `pedido@agenda.test` (`POST /coletivo/:id/pedido/aprovar`). Em seguida recusar o pedido de `membro@agenda.test` em `Produtora Beta` com motivo (`POST /coletivo/:id/pedido/recusar`). | Aprovação redireciona para `/coletivo/<id-beta>?msg=pedido-aprovado` (`aprovação registrada.`); se o Admin ainda não aprovou, status fica `aguardando o admin`. Recusa redireciona para `?msg=pedido-recusado` (`pedido recusado. a pessoa recebe um e-mail.`) e envia e-mail `decision` com motivo para `membro@agenda.test`. |
| `UAT-S6-05` | Esperado | `dono@agenda.test` / `1280×800` | Em `GET /coletivo/<id-alfa>`, promover `membro@agenda.test` (`POST /coletivo/:id/membro/promover`). Logar como `membro@agenda.test`, abrir `GET /coletivo/<id-alfa>` e testar: (a) tentar rebaixar `dono@agenda.test` (Master); (b) autorrebaixar-se (`POST /coletivo/:id/membro/rebaixar` com próprio `user_id`). | Promoção redireciona para `?msg=promovido` (`membro promovido a administrador.`). Botão de rebaixar não aparece para o Master; POST forçado contra o Master retorna `409` (`o master do coletivo não pode ser rebaixado.`). Autorrebaixamento redireciona para **`/coletivos?msg=voce-rebaixado`** (`você deixou de ser administrador deste coletivo.`), e `GET /coletivo/<id-alfa>` volta a responder `404` para `membro@agenda.test`. |
| `UAT-S6-06` | Esperado | `dono@agenda.test` / `1280×800` | Em `GET /coletivo/<id-alfa>`, renomear via `POST /coletivo/:id/nome` para `Coletivo Alfa Nordeste` e depois reverter para `Coletivo Alfa`. Testar também tentar renomear para um nome já ocupado (`Produtora Beta`). | Renomeação válida redireciona para `?msg=nome-alterado` (`nome alterado.`), envia `admin_request` (`Kind="rename"`) ao Admin e grava `coletivo renomeado` em `/admin/historico`. Nome ocupado retorna `422`. |
| `UAT-S6-07` | Borda | `dono@agenda.test` / CLI | Estando com coletivo congelado (`Locked=true` definido na Suíte 7), tentar promover, rebaixar ou renomear via POST direto. Testar também 11 pedidos em 1 hora (`Limits.Requests`). | Ações em coletivo congelado retornam `409` (`este coletivo está congelado pelo admin.`). 11ª requisição no limite retorna `429` (`msgTooMany`). |

---

### Suíte 7 — Painel do Admin, Aprovações, Bloqueios e Auditoria (`/admin/*`)

- **Escopo:** Fila de aprovações (`GET /admin` e 7 ações POST), gestão de contas (`GET /admin/contas`, bloquear/desbloquear), gestão de coletivos e bloqueios (`GET /admin/coletivos`, `POST /admin/coletivo/bloqueios`), histórico de auditoria (`GET /admin/historico`), rótulos de botão do e-mail `decision` (`"Entrar"`, `"Ver meus coletivos"`, `"Criar conta"`), acesso negado `403` para não-Admin e integridade referencial (`ACL-21`).
- **Persona:** `admin@agenda.test` (e `dono@agenda.test` para teste de `403`).
- **Pré-condições:** Contas pendentes `pendente-aprovacao@agenda.test`, `novo-coletivo@agenda.test`, `uat-novo1@agenda.test` e `pedido@agenda.test` presentes na fila `/admin`.
- **BDRs / ADRs:** `@BDR-004`, `@BDR-006`, `@ADR-001` (`ACL-21`).
- **Cenários:** `@BDD-S7-AdminGuard`, `@BDD-S7-ApproveFlows`, `@BDD-S7-RejectFlows`, `@BDD-S7-Blocks`, `@BDD-S7-AuditNoEmail`.
- **Rotas:** `GET /admin`, `POST /admin/pessoa/aprovar-tudo`, `POST /admin/conta/aprovar`, `POST /admin/conta/recusar`, `POST /admin/coletivo/aprovar`, `POST /admin/coletivo/recusar`, `POST /admin/pedido/aprovar`, `POST /admin/pedido/recusar`, `GET /admin/contas`, `POST /admin/conta/bloquear`, `POST /admin/conta/desbloquear`, `GET /admin/coletivos`, `POST /admin/coletivo/bloqueios`, `GET /admin/historico`.
- **Componentes:** Nenhum (formulários com IDs exclusivamente em campos `hidden`).

| ID | Tipo | Persona / Viewport | Passos Exatos | Resultado Observável Esperado |
|---|---|---|---|---|
| `UAT-S7-01` | Segurança | `dono@agenda.test` / CLI | Acessar `GET /admin`, `GET /admin/contas`, `GET /admin/coletivos`, `GET /admin/historico` e `POST /admin/conta/aprovar` autenticado como `dono@agenda.test` (não-Admin). | Todas retornam `403 Forbidden` com corpo `acesso negado`. |
| `UAT-S7-02` | Esperado | `admin@agenda.test` / `1280×800` | Abrir `GET /admin`. No card de `novo-coletivo@agenda.test` (conta pendente + `Coletivo Gama` pendente), clicar em aprovar tudo (`POST /admin/pessoa/aprovar-tudo`). | Redireciona para `/admin?msg=pessoa-aprovada` (`tudo aprovado para esta pessoa.`). Conta ativa, `Coletivo Gama` aprovado, vínculo aprovado. Mailpit envia `decision` com botão `"Entrar"` e nota de conta pendente (se aplicável). |
| `UAT-S7-03` | Esperado | `admin@agenda.test` / `1280×800` | No card de `pendente-aprovacao@agenda.test`, aprovar **apenas a conta** (`POST /admin/conta/aprovar`). Depois aprovar o pedido de entrada (`POST /admin/pedido/aprovar`). Testar também recusar um pedido (`POST /admin/pedido/recusar`) e recusar/aprovar um coletivo novo de conta ativa (`POST /admin/coletivo/aprovar` e `POST /admin/coletivo/recusar`). | Cada ação redireciona para o respectivo código em `adminNotices` (`conta-aprovada`, `pedido-aprovado`, `pedido-recusado`, `coletivo-aprovado`, `coletivo-recusado`). E-mail `decision` escolhe o botão certo: `"Entrar"` (decisão de conta aprovada), `"Ver meus coletivos"` (decisão para conta já ativa), `"Criar conta"` (conta recusada). |
| `UAT-S7-04` | Esperado | `admin@agenda.test` / `1280×800` | Criar e confirmar uma conta descartável `uat-reject@agenda.test` (com coletivo pendente `Coletivo UAT Descartado`), então recusar via `POST /admin/conta/recusar` com `reason="cadastro de teste"`. | Redireciona para `/admin?msg=conta-recusada` (`cadastro recusado.`). Usuário e coletivo pendente são removidos em cascata sem órfãos (`ACL-21`); e-mail `decision` enviado com motivo e botão `"Criar conta"`; entrada `cadastro recusado` gravada no histórico com snapshot de nome (sem e-mail). |
| `UAT-S7-05` | Borda | `admin@agenda.test` / CLI | Reenviar um `POST /admin/conta/aprovar` já decidido; enviar `reason` contendo caractere de controle `Cf` (`\u200B`, `ACL-67`). | Ação já decidida retorna `409 Conflict` (`isso já foi decidido; a lista foi atualizada.`). Motivo com `Cf` retorna `422 Unprocessable Entity`. |
| `UAT-S7-06` | Esperado | `admin@agenda.test` / `1280×800` | Abrir `GET /admin/contas`. Verificar que a própria linha do Admin não oferece botão de bloquear (e POST forçado retorna `409` `você não pode bloquear a própria conta.`). Bloquear `uat-disabled@agenda.test` (`POST /admin/conta/bloquear`) enquanto ela tem sessão ativa, testar a sessão dela, e depois desbloquear (`POST /admin/conta/desbloquear`). | Bloqueio redireciona para `/admin/contas?msg=conta-bloqueada` (`conta bloqueada.`), encerra imediatamente a sessão ativa de `uat-disabled@agenda.test` e não envia e-mail. Desbloqueio redireciona para `/admin/contas?msg=conta-desbloqueada` (`conta desbloqueada.`). |
| `UAT-S7-07` | Esperado | `admin@agenda.test` / `1280×800` | Abrir `GET /admin/coletivos`. Alterar checkboxes `events_blocked` e `locked` de um coletivo via `POST /admin/coletivo/bloqueios` com motivo e depois restaurar. | Redireciona para `/admin/coletivos?msg=bloqueios-salvos` (`bloqueios salvos.`). Entrada `bloqueios do coletivo` registrada em `/admin/historico`. |
| `UAT-S7-08` | Segurança | `admin@agenda.test` / `1280×800` | Abrir `GET /admin/historico` e inspecionar todas as linhas geradas até aqui. | Listagem em ordem decrescente (máximo 200 entradas), datas/horas em `dd/mm/aaaa HH:MM` (`America/Fortaleza`), rótulos em `pt-BR`, **nenhum endereço de e-mail** presente em qualquer entrada, e snapshots de contas/coletivos excluídos preservados. |

---

### Suíte 8 — Eventos: Criação, Edição, Exclusão, Meus Eventos, Widgets e Conflito 409

- **Escopo:** Formulário de criação/edição (`GET/POST /eventos/novo`, `GET/POST /eventos/:id/editar`), detalhe (`GET /eventos/:id`), exclusão (`POST /eventos/:id/excluir`), `GET /meus-eventos`, cascatas `GET /opcoes/cidades` e `GET /opcoes/subestilos`, 5 widgets Alpine (`datePicker`, `timeSpinner`, `durationSpinner`, `moneyInput`, `eventDynamic`), matriz de bloqueio `EventsBlocked` (403 vs 409 + override do Admin), concorrência otimista (`version` -> 409), evento que vira a meia-noite em `APP_TZ`, validação de `ticket_url` e auditoria seletiva.
- **Persona:** `dono@agenda.test`, `membro@agenda.test`, `delta@agenda.test` (coletivo bloqueado), `admin@agenda.test`.
- **Pré-condições:** `Coletivo Alfa` e `Produtora Beta` ativos; `Coletivo Delta` com `EventsBlocked=true` e evento `Noite Ambient no Terraço`.
- **BDRs / ADRs:** `@BDR-005`, `@BDR-006`, `@ADR-001` (`ACL-21`).
- **Cenários:** `@BDD-S8-Widgets`, `@BDD-S8-CreateEditDelete`, `@BDD-S8-BlockedMatrix`, `@BDD-S8-OptimisticLock`, `@BDD-S8-MidnightAndTZ`.
- **Rotas:** `GET /eventos/novo`, `POST /eventos/novo`, `GET /eventos/:id/editar`, `POST /eventos/:id/editar`, `GET /eventos/:id`, `POST /eventos/:id/excluir`, `GET /meus-eventos`, `GET /opcoes/cidades`, `GET /opcoes/subestilos`, `GET /eventos/dia`.
- **Componentes:** `datePicker`, `timeSpinner`, `durationSpinner`, `moneyInput`, `eventDynamic`.

| ID | Tipo | Persona / Viewport | Passos Exatos | Resultado Observável Esperado |
|---|---|---|---|---|
| `UAT-S8-01` | Esperado | `dono@agenda.test` / `1280×800` | Abrir `GET /eventos/novo`. Testar todos os 5 widgets: (1) `datePicker`: abrir `[▦]`, navegar com setas/`PageUp`/`PageDown`, confirmar que dias passados têm `aria-disabled="true"` e escolher data futura; (2) `timeSpinner`: digitar `2330` (vira `23:30`), testar `ArrowUp`/`PageUp`, editar no meio preservando caret (`caretAfterRewrite`) e digitar hora inválida `29`; (3) `durationSpinner`: testar limites `0h15` (`a duração mínima é de 15 minutos`) e `23h59` (`a duração máxima é de 23h59`); (4) `moneyInput`: digitar `2550` -> `25,50`, testar limite `R$ 99.999,99`, marcar `#free` (esvazia e desabilita `#price`); (5) `eventDynamic`: trocar `#state` -> `CE` (fetch `/opcoes/cidades`), trocar `#style` -> `techno` (fetch `/opcoes/subestilos`), selecionar `outros` em `#type` e `#style` (exibe `#type_other`/`#style_other` e desabilita subestilo). | Todos os 5 widgets funcionam sem erros JS nem violações CSP. `/opcoes/cidades` e `/opcoes/subestilos` retornam `200` com `Cache-Control: no-store`. Botão `[↻] ATUALIZAR LISTAS` permanece oculto enquanto os fetches funcionam. |
| `UAT-S8-02` | Esperado | `dono@agenda.test` / `1280×800` | Em `GET /eventos/novo`, criar evento `Festa UAT Meia-Noite` em `Coletivo Alfa`, início `23:30`, duração `4h00`, preço `R$ 35,00`, `ticket_url="https://ingressos.example.com/uat"`, `CE`/`Fortaleza`. | Redireciona `303` para `/eventos/<id>?msg=evento-criado` (`evento criado.`). Evento permanece no dia de início em `America/Fortaleza` mesmo terminando às `03:30` do dia seguinte. Link de ingresso renderiza com `rel="noopener noreferrer nofollow ugc"` e `target="_blank"`. Nenhuma entrada de criação no `/admin/historico`. |
| `UAT-S8-03` | Borda | `dono@agenda.test` / `1280×800` | Em `POST /eventos/novo`, testar: (a) data anterior a hoje; (b) `ticket_url="javascript:alert(1)"` ou `ftp://x.com`; (c) nome com Unicode `Cf` (`\u200B`, `ACL-67`). | Todos retornam `422`: (a) `a data não pode ser anterior a hoje`; (b) `informe um endereço que comece com http:// ou https://`; (c) erro de controle `MsgControl`. |
| `UAT-S8-04` | Esperado | `dono@agenda.test` / `1280×800` | Abrir `GET /eventos/<id-passado>/editar` (ex.: `Warm Up Techno`, de `-8` dias), alterar apenas a descrição mantendo a data passada e salvar (`POST /eventos/:id/editar`). | Redireciona `303` para `/eventos/<id>?msg=evento-salvo` (`alterações salvas.`). Edição permite manter a própria data passada (`data-keep`). Nenhuma entrada no `/admin/historico` (edição pelo próprio Administrador). |
| `UAT-S8-05` | Borda | `dono@agenda.test` / `1280×800` | Abrir `GET /eventos/<id-uat>/editar` em duas abas (Aba A e Aba B, ambas com `version=1`). Salvar alteração na Aba A (`version` vai para `2`). Em seguida tentar salvar na Aba B (enviando `version=1`). | Aba B recebe `409 Conflict` com alerta `este evento foi alterado por outra pessoa; confira e salve de novo.`, link para o evento atualizado e campo oculto `version` atualizado para `2` para permitir salvamento deliberado. |
| `UAT-S8-06` | Segurança | `membro@agenda.test`, `delta@agenda.test`, `admin@agenda.test` / `1280×800` | Validar matriz `EventsBlocked`: (a) `membro@agenda.test` tenta `GET /eventos/novo`, `GET /eventos/<id>/editar`, `POST /eventos/<id>/excluir`; (b) `delta@agenda.test` (único coletivo bloqueado) tenta `GET /eventos/novo`, `GET /eventos/<id-delta>/editar`, `POST /eventos/<id-delta>/excluir`; (c) `admin@agenda.test` edita `Noite Ambient no Terraço` (de `Coletivo Delta`, bloqueado) e exclui `Festa UAT Meia-Noite`. | (a) `membro`: `403` em tudo; sidebar exibe `Criar Evento` desabilitado (`aria-disabled="true"`). (b) `delta`: `GET /eventos/novo` retorna `403` (nenhum coletivo disponível); `GET /eventos/<id-delta>/editar` e `POST /eventos/<id-delta>/excluir` retornam `409 Conflict` com `os eventos deste coletivo estão bloqueados pelo admin.`; em `GET /eventos/<id-delta>`, nota de somente leitura aparece só para `delta`. (c) `admin`: edita com sucesso (grava `evento editado pelo admin` no `/admin/historico`) e exclui com sucesso (`303` para `/?msg=evento-excluido`, grava `evento excluído` com nome + data `APP_TZ`). |
| `UAT-S8-07` | Esperado | `dono@agenda.test`, `membro@agenda.test`, `delta@agenda.test` / `1280×800` | Abrir `GET /meus-eventos` com cada uma das 3 personas. | Separação correta entre `Próximos` (ordem crescente) e `Anteriores` (mais recente primeiro). `dono` vê botões de editar; `membro` vê nota `só leitura (Membro)`; `delta` vê nota `eventos bloqueados pelo admin`. |

---

### Suíte 9 — Calendário Mensal, Filtros, Sheet HTMX e Analisar Data (`calendarPage`)

- **Escopo:** Página principal (`GET /`), fragmento mensal (`GET /calendario`), lista diária (`GET /eventos/dia`), componente `calendarPage` (filas `newestOnly` independentes, `HANG_WAIT` 8s, histórico `pushState`/Back/Forward, validação `offers`, filtros UF/cidade, roving tabindex com setas/`Home`/`End`, modal `<dialog id="evento-sheet">`, links `+N`, comportamento mobile `< 640px` vs desktop `>= 640px`), e painel `Analisar Data` (`#analise-resultado`, destaque mesma cidade e tratamento 422).
- **Persona:** `dono@agenda.test` (e `membro@agenda.test`).
- **Pré-condições:** Eventos do `seed-dev` distribuídos no mês atual (incluindo dia `+3` com 3 eventos: 2 em Fortaleza/CE e 1 em Recife/PE) e mês seguinte.
- **BDRs / ADRs:** `@BDR-005`, `@BDR-007` (contratos de `AGENTS.md`).
- **Cenários:** `@BDD-S9-MonthNav`, `@BDD-S9-Filters`, `@BDD-S9-EventSheet`, `@BDD-S9-DayAnalysis`, `@BDD-S9-RaceQueues`.
- **Rotas:** `GET /`, `GET /calendario`, `GET /eventos/dia`, `GET /eventos/:id`.
- **Componentes:** `calendarPage`, `eventDynamic` (painel `Analisar Data`).

| ID | Tipo | Persona / Viewport | Passos Exatos | Resultado Observável Esperado |
|---|---|---|---|---|
| `UAT-S9-01` | Esperado | `dono@agenda.test` / `1280×800` | Abrir `GET /`. Clicar em Próximo Mês, Anterior e `Hoje`. Usar botões Voltar e Avançar do navegador. | Trocas de mês carregam via `GET /calendario` (`Cache-Control: no-store`) dentro de `#calendar-slot` sem recarregar a página, atualizam URL via `history.pushState`, preservam foco no controle equivalente e anunciam o mês em `#calendar-status`. Voltar/Avançar restaura mês e filtros sem duplicar histórico. |
| `UAT-S9-02` | Esperado | `dono@agenda.test` / `1280×800` | No calendário, selecionar `uf=CE` em `#calendar-uf` e depois `cidade=Fortaleza` em `#calendar-cidade`. Verificar botão `FILTRAR` oculto via `.calendar-page--js`. Navegar para o próximo mês mantendo o filtro. Testar URL manual `/?mes=2026-11&uf=XX&cidade=Fake`. | Filtro aplica automaticamente ao mudar select, mantendo `uf` e `cidade` nos links de navegação e nos links de dia (`/eventos/dia?data=...&uf=CE&cidade=Fortaleza`). URL com UF/cidade inválida cai com segurança para "todos" sem erro 500. |
| `UAT-S9-03` | Esperado | `dono@agenda.test` / `1280×800` | Clicar com botão esquerdo simples em um chip de evento no calendário. Depois fechar com `Escape` e com `[✕]`. Testar também `Ctrl+Click` (ou botão do meio) em um chip. | Clique simples faz `GET /eventos/:id` (HTMX, `Cache-Control: no-store`) e abre `<dialog id="evento-sheet">` com `aria-labelledby`. Fechar devolve foco ao chip original (ou ao dia com `tabindex="0"`). `Ctrl+Click` abre `/eventos/:id` em nova aba como link comum. |
| `UAT-S9-04` | Esperado | `dono@agenda.test` / `1280×800` | Testar roving tabindex nos números de dia (`.calendar__daynum`): usar `ArrowLeft`, `ArrowRight`, `ArrowUp`, `ArrowDown`, `Home` e `End`. No dia `+3` (3 eventos), clicar no número do dia ou link `+N`. | Apenas 1 dia por vez tem `tabindex="0"`; setas/`Home`/`End` movem o foco corretamente pela grade. Em `>= 640px`, clicar no dia navega para `GET /eventos/dia?data=<AAAA-MM-DD>` listando os 3 eventos em ordem cronológica. |
| `UAT-S9-05` | Esperado | `dono@agenda.test` / `1280×800` | Em `GET /eventos/novo`, preencher data igual ao dia `+3`, `state=CE`, `city=Fortaleza` e clicar em `ANALISAR DATA`. Depois alterar a cidade para `Sobral` sem clicar novamente. Testar também `GET /eventos/dia?data=invalida` (via HTMX no painel e via navegação direta). | Painel `#analise-resultado` carrega via HTMX (`GET /eventos/dia?data=...&destaque_uf=CE&destaque_cidade=Fortaleza`), colocando os 2 eventos de Fortaleza primeiro com destaque de mesma cidade (`SameCity`). Ao trocar cidade, o painel exibe aviso de desatualizado (`panelStale`) sem recarregar sozinho. Data inválida retorna `422` com `escolha uma data válida (dd/mm/aaaa)` injetado no painel (via `htmx:beforeSwap` restrito a `#analise-resultado` + `422`) e link de volta preservando filtro na página cheia. |
| `UAT-S9-06` | Borda | `dono@agenda.test` / `1280×800` | Com rede desacelerada (DevTools throttling `Slow 3G`), clicar rapidamente 3 vezes em Próximo Mês; e simular resposta pendente `> 8s` (`HANG_WAIT`). | Fila `newestOnly` garante que apenas o último mês clicado vence e configura a tela (`latest()`). Se uma requisição travar além de `8000 ms`, `calendarPage` faz fallback automático para o link comum (`location.replace` / `location.assign`) sem travar a interface. |

---

### Suíte 10 — E-mails Transacionais (Mailpit), Outbox e Auditoria End-to-End

- **Escopo:** Verificação completa dos **10 templates de e-mail** no Mailpit (headers `From`, `Subject`, partes `text/plain` e `text/html`, escape HTML contra XSS, links absolutos `APP_ORIGIN`), validação SecOps de e-mail/outbox (`ACL-32`, `ACL-37`, `ACL-38`, `ACL-48`), expurgo diário de cadastros não confirmados (`ACL-39` / `PurgeUnconfirmedSignups`) e auditoria sem PII de e-mail.
- **Persona:** `admin@agenda.test` + inspeção na Web UI / API do Mailpit (`http://127.0.0.1:8025`).
- **Pré-condições:** Suítes 1 a 9 executadas (todos os 10 templates disparados ao menos uma vez).
- **BDRs / ADRs:** `@BDR-001` (`ACL-39`, `ACL-48`), `@BDR-006`, `@ADR-001` (`ACL-21`).
- **Cenários:** `@BDD-S10-All10Emails`, `@BDD-S10-OutboxDelivery`, `@BDD-S10-DailyJobs`, `@BDD-S10-AuditPrivacy`.
- **Rotas:** `GET /admin/historico` + Mailpit API `GET http://127.0.0.1:8025/api/v1/messages`.
- **Componentes:** Nenhum.

| ID | Tipo | Persona / Viewport | Passos Exatos | Resultado Observável Esperado |
|---|---|---|---|---|
| `UAT-S10-01` | Esperado | Mailpit / CLI | Consultar `http://127.0.0.1:8025` e conferir a presença e renderização (`text/plain` e `text/html`) de todos os **10 templates**: `verify_email`, `existing_account`, `admin_new_signup`, `password_reset`, `decision`, `owner_join_request`, `admin_request` (subtipos `join`, `new_collective`, `rename`), `change_email`, `email_change_notice` e `password_expiring`. | Todos os 10 templates entregues com `From: IgnisDev <ignisdev@magalz.space>`, assuntos em `pt-BR` sem quebras de linha, links iniciando com `http://localhost:8120/`, e ambas as partes MIME (`text/plain` e `text/html`) legíveis e sem tags quebradas. |
| `UAT-S10-02` | Segurança | Mailpit / CLI | (`ACL-48`) Inspecionar os e-mails `verify_email` gerados na Suíte 1 e Suíte 2, e compará-los com `existing_account`. Verificar também se caracteres `<script>` ou `"` em nomes/motivos de `decision` ou `admin_new_signup` foram escapados no HTML. | `verify_email` usa exclusivamente saudação `Olá!` sem o nome submetido; `existing_account` exibe o nome já salvo na conta. Todo texto de usuário no HTML está escapado (`&lt;script&gt;`), nunca renderizado como HTML bruto. |
| `UAT-S10-03` | Segurança | Mailpit + Logs | (`ACL-32`, `ACL-37`, `ACL-38`) Inspecionar logs de `agenda serve` e coleção `outbox` no MongoDB (`agenda_uat`). | Todas as mensagens da outbox foram entregues exatamente **1 vez** (nenhuma duplicata por erro de `QUIT` — `ACL-38`), nenhuma mensagem presa em loop de lease (`ACL-37`), nenhum vazamento de conexão SMTP (`ACL-32`) e nenhuma credencial/token/corpo nos logs estruturados `slog`. |
| `UAT-S10-04` | Esperado | CLI / MongoDB | (`ACL-39` / `PurgeUnconfirmedSignups` + `SendPasswordReminders`) Inspecionar logs do `dailyLoop` no startup de `agenda serve` (ou inserir um usuário `pending_email` com `created_at` de 8 dias atrás + coletivo `pending` e reiniciar `serve`). | `dailyLoop` executa ambos os jobs de forma independente e idempotente, registrando `daily: password reminders sent count=...` e `daily: unconfirmed signups purged count=...` (apenas `count`, sem PII). Usuário não confirmado `> 7` dias e seu coletivo pendente são removidos sem gerar auditoria nem e-mail. |
| `UAT-S10-05` | Segurança | `admin@agenda.test` / `1280×800` | Abrir `GET /admin/historico` e buscar pela string `@` no DOM da página. | Nenhuma entrada de auditoria contém `@` ou qualquer endereço de e-mail; todas as ações administrativas, exclusões de eventos e edições feitas pelo Admin em coletivos alheios estão listadas corretamente. |

---

### Suíte 11 — Responsividade (3 Viewports), Progressive Enhancement (Sem-JS), Acessibilidade e Desempenho

- **Escopo:** Validação visual e funcional nos 3 viewports (`1280×800`, `768×1024`, `375×812`), drawer mobile `shell` (focus trap `Tab`/`Shift+Tab`, `Escape`, `:inert="mainInert"`), navegação de calendário mobile (`< 640px` carregando `#calendar-day`), uso completo **com JavaScript desabilitado** (`[↻] ATUALIZAR LISTAS`, links `303`, filtro `FILTRAR`), foco `2px solid #fff`, alvos `>= 24px`, `prefers-reduced-motion: reduce` e auditoria final de latência/console.
- **Persona:** `dono@agenda.test`, `admin@agenda.test`.
- **Pré-condições:** Stack UAT ativa; DevTools pronto para alternar viewports, desabilitar JS e emular `prefers-reduced-motion: reduce`.
- **BDRs / ADRs:** `@BDR-007` (acessibilidade e progressive enhancement em `AGENTS.md`).
- **Cenários:** `@BDD-S11-Viewports`, `@BDD-S11-DrawerFocusTrap`, `@BDD-S11-NoJS`, `@BDD-S11-A11yMotion`, `@BDD-S11-Latency`.
- **Rotas:** `GET /`, `GET /eventos/novo`, `POST /eventos/novo`, `GET /eventos/:id`, `GET /coletivos`, `GET /perfil`, `GET /admin`.
- **Componentes:** `shell`, `calendarPage`, `eventDynamic`, `datePicker`.

| ID | Tipo | Persona / Viewport | Passos Exatos | Resultado Observável Esperado |
|---|---|---|---|---|
| `UAT-S11-01` | UX/A11y | `dono@agenda.test` / `1280×800`, `768×1024`, `375×812` | Percorrer `/`, `/eventos/novo`, `/meus-eventos`, `/coletivos`, `/perfil` e `/admin` (como Admin) nos 3 viewports. | Em `1280×800`: sidebar fixa lateral, sem overflow horizontal. Em `768×1024` e `375×812` (`< 1024px`): topbar com botão `[≡] menu` e sidebar em modo drawer; em `375×812` (`< 640px`), tocar no número de um dia no calendário carrega a lista em `#calendar-day` abaixo da grade sem sair da página. |
| `UAT-S11-02` | UX/A11y | `dono@agenda.test` / `375×812` | Abrir o drawer mobile clicando em `[≡] menu` (`shell`). Pressionar `Tab` e `Shift+Tab` repetidamente; inspecionar `<main id="conteudo">`; pressionar `Escape`; redimensionar de `375×812` para `1280×800` com o drawer aberto. | Enquanto aberto abaixo de `1024px`: `<main>` recebe atributo `inert`, botão muda para `[✕] fechar` (`aria-expanded="true"`), e `Tab`/`Shift+Tab` ficam presos dentro de `#menu-lateral`. `Escape` fecha o drawer e devolve o foco ao botão `[≡] menu`. Ao cruzar `1024px`, o drawer fecha automaticamente sem mover foco para o toggle oculto. |
| `UAT-S11-03` | Esperado | `dono@agenda.test` / `1280×800` (JS **desabilitado**) | Desabilitar JavaScript no navegador. (1) Em `GET /`, usar o filtro UF + clicar no botão `FILTRAR` e navegar pelos links de mês e chips; (2) Em `GET /eventos/novo`, selecionar `state=PE` e `style=house`, clicar em `[↻] ATUALIZAR LISTAS` (`name="acao" value="atualizar"`), conferir listas preenchidas sem salvar, preencher o restante e enviar `POST /eventos/novo`. | (1) Botão `FILTRAR` fica visível e filtra via `GET /?mes=...&uf=...`; links de mês, dias e chips funcionam normalmente. (2) `[↻] ATUALIZAR LISTAS` retorna `200` com cidades de PE e subestilos de `house` preenchidos, sem erros de validação e sem gravar no banco; o envio final cria o evento com redirect `303`. |
| `UAT-S11-04` | UX/A11y | `dono@agenda.test` / `1280×800` | Navegar via teclado (`Tab`) por links, botões, inputs e dias do `datePicker`. Ativar `prefers-reduced-motion: reduce` no DevTools e navegar entre meses no calendário. | Todos os controles focáveis exibem `outline: 2px solid #fff; outline-offset: 2px` (exceção documentada: `.date-picker__day:focus-visible` usa `outline-offset: -2px` para não invadir o dia vizinho). Alvos interativos medem `>= 24px`. Com `prefers-reduced-motion: reduce`, transições de slide de mês e animações de stagger são desativadas. |
| `UAT-S11-05` | Desempenho | `dono@agenda.test` / `1280×800` | Consolidar métricas da aba Network e Console ao final de todas as suítes (`Suíte 0` a `Suíte 11`). | `0` erros no Console JS, `0` violações CSP, `0` respostas `5xx`, `0` assets `404`, rotas `GET`/HTMX `<= 300 ms`, rotas `POST` com Argon2id `<= 1500 ms`. |

---

## 4. Checklist de Fechamento do UAT (Go / No-Go)

Antes de liberar o deploy em produção, confirme todos os itens abaixo:

- [ ] Todas as **12 suítes (`Suíte 0` a `Suíte 11`)** executadas na stack isolada (`agenda-uat-mongo` `:27019`, `agenda-uat-mailpit` `:1025`/`:8025`, `http://localhost:8120`).
- [ ] **61 handlers / 50 caminhos HTTP únicos** validados sem `5xx`.
- [ ] **10 componentes `Alpine.data` (CSP)** + fluxos **HTMX 2** validados com `0` erros JS e `0` violações CSP.
- [ ] **10 templates de e-mail** conferidos no Mailpit (`text/plain` e `text/html`).
- [ ] **10 itens de SecOps (`ACL-21`, `ACL-32`, `ACL-37`, `ACL-38`, `ACL-39`, `ACL-41`, `ACL-43`, `ACL-48`, `ACL-49`, `ACL-67`)** verificados.
- [ ] **3 viewports (`1280×800`, `768×1024`, `375×812`)** e fluxo **Sem-JS** aprovados.
- [ ] Todos os defeitos encontrados (se houver) registrados no Backlog `ACL` do ItsaPlan com label `found-by-agent`.
