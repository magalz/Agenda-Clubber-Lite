---
id: BDR-003
title: "Edição de Perfil e Troca de E-mail em Duas Etapas"
status: accepted
date: 2026-11-26
superseded_by: null
issues: [ACL-126, ACL-127]
scope: [internal/accounts, internal/domain, internal/mail, internal/web]
---

# BDR-003: Edição de Perfil e Troca de E-mail em Duas Etapas

## Contexto de Negócio

Pessoas aprovadas no Agenda Clubber Lite precisam manter atualizados seu nome de exibição, telefone de contato (incluindo indicação de WhatsApp) e endereço de e-mail sem depender de intervenção manual do Admin. Como o e-mail é a credencial de login e o canal de notificações de aprovação e recuperação de senha, a alteração de endereço exige confirmação explícita da posse do novo e-mail e aviso preventivo ao endereço atual, sem permitir enumeração de endereços de outras contas nem interromper sessões ativas.

## Regra Acordada

### 1. Edição de Perfil (`GET/POST /perfil`, `accounts.UpdateProfile`)
- **Campos editáveis:** a pessoa autenticada pode atualizar `Name`, `Phone` e `PhoneIsWhatsApp` (`phone_is_whatsapp == "1"`). O e-mail atual é exibido apenas para leitura na página `/perfil`.
- **Regras de validação:**
  - `Name`: espaços nas extremidades removidos (`strings.TrimSpace`) e validado por `domain.TextProblem(name, domain.UserNameMin, domain.UserNameMax, false)` (`1`–`120` runas, UTF-8 válido, linha única sem caracteres de controle, formato Unicode `Cf` ou separadores `U+2028`/`U+2029`).
  - `Phone`: somente dígitos ASCII são mantidos via `accounts.PhoneDigits`, exigindo de **10 a 11 dígitos** (`domain.PhoneMin = 10`, `domain.PhoneMax = 11`).
  - O restante do estado é checado por `User.Validate()` antes da persistência; `Users.UpdateProfile` no repositório é uma escrita filtrada que não revalida campos por conta própria.
- **Sem perda de sessão e sem auditoria:** sessões são indexadas por `UserID`, portanto permanecem ativas após a atualização; alterações de perfil em autosserviço não geram entrada no log de auditoria.
- **Resposta:** sucesso redireciona para `/perfil?msg=perfil-salvo` (`303` em requisição comum ou `204` com `HX-Redirect` em HTMX); erros de campo retornam `422` preservando os valores digitados.

### 2. Troca de E-mail em Duas Etapas (`POST /perfil/email`, `GET/POST /confirmar-email`)
- **Etapa 1 — Solicitação (`accounts.RequestEmailChange`):**
  - Protegida por `Limits.EmailAction` (`5/hora` por IP, prefixo `perfil-email`) e `Limits.EmailActionEmail` (`5/hora` por hash do e-mail informado) via `emailForm`.
  - O endereço é normalizado por `domain.NormalizeEmail`.
  - **Próprio e-mail atual:** se o endereço normalizado for igual a `actor.Email`, retorna erro de campo (`email`: `"este já é o seu e-mail"`) com status `422`.
  - **E-mail ocupado por outra conta (anti-enumeração):** se outra conta já usar o endereço, o serviço enfileira apenas `TemplateExistingAccount` para o titular real (com o nome do titular e links de `/entrar` e `/senha/esqueci`), **não** cria token, **não** avisa o e-mail antigo do solicitante e retorna `nil`, resultando no mesmo redirecionamento `/perfil?msg=email-pendente`.
  - **E-mail disponível:** cria token `TokenPurposeChangeEmail` (`change_email`) com `NewEmail` preenchido e validade de **24 horas** (`ChangeEmailTokenTTL = 24 * time.Hour`). `Tokens.Create` aposenta qualquer token `change_email` anterior ainda não utilizado daquela conta. Na mesma `WithTx`, enfileira dois e-mails:
    1. `TemplateChangeEmail` para o novo endereço com o link `<APP_ORIGIN>/confirmar-email?token=<raw>`.
    2. `TemplateEmailChangeNotice` para o endereço atual (`actor.Email`), informando o pedido de troca sem link de ação; o endereço atual continua ativo até a confirmação.
- **Etapa 2 — Confirmação (`GET/POST /confirmar-email`, `accounts.ConfirmEmailChange`):**
  - Rota **pública** (o link pode ser aberto em outro dispositivo ou sem sessão ativa).
  - `GET /confirmar-email` apenas renderiza a página com botão de confirmação e o token em campo oculto, sem consultar o banco nem consumir o token (proteção contra scanners de e-mail e pré-visualizações de link).
  - `POST /confirmar-email` é limitado por `Limits.Login` (`10/minuto` por IP, chave `confirm:<ip>`). Em uma única `WithTx`, consome o token (`Tokens.Consume` com `TokenPurposeChangeEmail`) e grava o novo e-mail (`Users.SetEmail`), sem enviar um segundo e-mail e sem gravar entrada no log de auditoria.
  - Sessões continuam válidas após a troca por serem associadas ao `UserID`.
  - Redireciona sempre para `/perfil?msg=<código>`; caso o visitante esteja deslogado, `requireAuth` preserva `?next=/perfil?msg=<código>` no login.

### 3. Tabela Fixa de Avisos (`perfilNotices` em `internal/web/routes_profile.go`)
A página `/perfil` só exibe textos da tabela fixa `perfilNotices`; códigos desconhecidos em `?msg=` são ignorados e nunca ecoados:

| Código (`?msg=`) | Tipo visual | Texto exibido |
| :--- | :--- | :--- |
| `perfil-salvo` | Status (`.status--ok`) | `"perfil salvo."` |
| `email-pendente` | Status (`.status--ok`) | `"se o e-mail estiver disponível, enviamos um link de confirmação. ele vale por 24 horas."` |
| `email-confirmado` | Status (`.status--ok`) | `"e-mail confirmado. seu endereço foi atualizado."` |
| `email-invalido` | Alerta de erro (`.status--error`) | `"este link expirou ou já foi usado."` |
| `email-em-uso` | Alerta de erro (`.status--error`) | `"este e-mail já está em uso por outra conta. peça a troca novamente."` |

## Exceções e Casos de Borda

- **Endereço ocupado no intervalo entre solicitação e confirmação:** se outra conta registrar ou assumir o endereço entre `RequestEmailChange` e `ConfirmEmailChange`, `Users.SetEmail` retorna `domain.ErrEmailTaken`, a transação sofre rollback e o handler redireciona para `/perfil?msg=email-em-uso`. `Users.SetEmail` ignora a própria conta na checagem de unicidade.
- **Link malformado, expirado, já usado ou conta removida:** `ConfirmEmailChange` retorna `ErrEmailChangeInvalid` (sem consultar o banco quando `auth.HashToken` falha) e redireciona para `/perfil?msg=email-invalido`.
- **Rate limit em `POST /confirmar-email` (`429`):** mantém o token no campo oculto da página de confirmação para que a pessoa possa tentar novamente com o mesmo link após aguardar.
- **Privacidade em páginas e logs:** o endereço de outra conta e o token nunca são ecoados na página de perfil, na URL ou nos logs.

## Prova Executável (BDD)

- **Serviços (`internal/accounts`):**
  - `internal/accounts/profile_test.go`: `TestUpdateProfileValidatesAndSaves`, `TestUpdateProfileRefusesControlCharactersInName`, `TestRequestEmailChangeRefusesOwnAddress`, `TestRequestEmailChangeOccupiedAddressNotifiesOnlyItsOwner`, `TestRequestEmailChangeMailsBothAddressesAndCreatesToken`, `TestRequestEmailChangeRetiresTheOlderToken`, `TestConfirmEmailChangeSwapsTheEmailAndKeepsSessions`, `TestConfirmEmailChangeRefusesBadLinks`
- **Templates de e-mail (`internal/mail`):**
  - `internal/mail/render_test.go`: `TestRenderChangeEmailKeepsTheLinkInPlace`, `TestRenderEmailChangeNoticeHasNoActionLink`
- **Rotas HTTP (`internal/web`):**
  - `internal/web/routes_profile_test.go`: `TestProfilePageShowsTheSignedInPerson`, `TestProfilePageNeedsASession`, `TestProfileNoticesComeFromTheFixedTable`, `TestProfilePostsNeedTheAppsOrigin`, `TestProfileSaveUpdatesTheAccountAndRedirects`, `TestProfileSaveRefusesBadFieldsAndKeepsThem`, `TestProfileSaveRedirectsHTMXRequests`, `TestProfileEmailChangeEnqueuesAndRedirects`, `TestProfileEmailChangeLimitsTheForm`, `TestProfileEmailChangeLimitsPerEmail`, `TestProfileEmailChangeRefusesTheCurrentAddress`, `TestProfileConfirmLinkShowsAButtonAndSpendsNothing`, `TestProfileConfirmChangesTheEmailAndRedirects`, `TestProfileConfirmWorksForAnAnonymousVisitor`, `TestProfileConfirmRefusesAnInvalidLink`, `TestProfileConfirmPostRateLimitPerIP`, `TestProfileConfirmReportsAnInterimTakenAddress`, `TestProfilePagesShowNoOtherAddress`
- **Cobertura BDD / UAT:** `@BDD-02-01` a `@BDD-02-09`, `UAT-S5-01` a `UAT-S5-06`.
