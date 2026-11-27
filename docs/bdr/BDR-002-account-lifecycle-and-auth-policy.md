---
id: BDR-002
title: "Ciclo de Vida de Contas e Política de Autenticação"
status: accepted
date: 2026-11-26
superseded_by: null
issues: [ACL-126, ACL-127, ACL-41]
scope: [internal/accounts, internal/auth, internal/domain, internal/web]
---

# BDR-002: Ciclo de Vida de Contas e Política de Autenticação

## Contexto de Negócio

O Agenda Clubber Lite é um calendário fechado de coletivos e produtoras do Nordeste: nenhuma página de eventos é pública e toda pessoa precisa cadastrar-se, confirmar a posse do e-mail e passar pela aprovação do Admin antes de acessar o calendário. Como o sistema lida com credenciais pessoais, links de uso único enviados por e-mail e sessões persistentes, as regras de cadastro, confirmação, login, expiração/histórico de senhas, recuperação de acesso e limitação de taxa devem impedir enumeração de contas, ataques de força bruta, redirecionamento aberto e acúmulo de cadastros abandonados, mantendo clareza operacional em português (`pt-BR`).

Decisões específicas do backlog SecOps sobre enumeração de coletivos no cadastro (`ACL-39`), bloqueio compartilhado na troca/verificação de senha (`ACL-43`) e saudação sem nome em `verify_email.tmpl` (`ACL-48`) estão registradas em `BDR-001` e não são duplicadas aqui.

## Regra Acordada

### 1. Cadastro (`POST /cadastro`, `accounts.Signup`, `accounts.ValidateSignup`)
- **Modos de vínculo inicial:** ao se cadastrar, a pessoa informa nome (`1`–`120` runas sem caracteres de controle), e-mail (normalizado via `domain.NormalizeEmail`: minúsculas, sem espaços, domínio com ponto, `3`–`254` runas), telefone (`10`–`11` dígitos ASCII extraídos por `PhoneDigits` + flag `PhoneIsWhatsApp`), senha + confirmação, tipo (`coletivo` ou `produtora`), papel (`owner` ou `member`) e coletivo:
  - **Entrar em coletivo existente:** seleciona um coletivo aprovado via `CollectiveID` (autocomplete `/coletivos/sugestoes`, até 8 itens, consulta de 2 a 80 runas) ou digita exatamente um nome correspondente a um coletivo aprovado via `domain.NameKey`. O coletivo deve estar `CollectiveStatusApproved`, ter o mesmo `CollectiveKind` escolhido e não estar congelado (`!Locked`). A transação reescreve o coletivo (`Collectives.Update`) para serializar contra congelamento concorrente.
  - **Criar coletivo novo:** quando o nome digitado não existe no banco (`ErrNotFound` em `ByNameKey`), o cadastro cria um coletivo `CollectiveStatusPending` com a pessoa como `CreatedBy` (Master) e um vínculo `MembershipStatusPending` já com `OwnerApprovedBy/At` preenchidos. **Somente `RoleOwner` (Administrador)** pode criar coletivo novo; escolher `RoleMember` retorna erro de campo (`"quem cria um coletivo novo entra como administrador (master)"`).
- **Pré-validação antes do orçamento por e-mail:** `POST /cadastro` chama `accounts.ValidateSignup` antes de debitar `Limits.SignupEmail`, impedindo que submissões inválidas esgotem o orçamento de um e-mail alvo (cf. `ADR-002`).
- **Anti-enumeração de e-mail:** todas as validações de campo e a resolução do coletivo/papel ocorrem antes da consulta de `Users.ByEmail`. Se o e-mail já existir (inclusive em corrida que retorna `domain.ErrEmailTaken`), o serviço apenas enfileira `TemplateExistingAccount` para o titular real e retorna `nil`, exibindo a mesma página `/cadastro/enviado` sem revelar que o e-mail está cadastrado.
- **Estado inicial e confirmação:** a conta nasce em `UserStatusPendingEmail`, com token `TokenPurposeVerifyEmail` válido por **24 horas** (`SignupTokenTTL = 24 * time.Hour`) e e-mail `TemplateVerifyEmail` enfileirado na mesma transação (saudação sem nome conforme `BDR-001` / `ACL-48`). O Admin não é notificado antes da confirmação do e-mail.

### 2. Confirmação de E-mail, Reenvio e Purga (`GET/POST /confirmar`, `POST /confirmar/reenviar`)
- **Proteção contra scanners de e-mail:** `GET /confirmar?token=...` apenas renderiza o botão de confirmação com o token em campo oculto, sem consultar o banco nem consumir o token. Sem `token`, responde `400` com formulário de reenvio.
- **Consumo do token (`ConfirmEmail`):** `POST /confirmar` consome o token (`Tokens.Consume`), promove a conta de `pending_email` para `pending_approval` (`Users.MarkEmailVerified`) e enfileira `TemplateAdminNewSignup` para todos os admins ativos, tudo em uma única `WithTx`. Sucesso redireciona para `/aguardando`.
- **Reenvio silencioso (`ResendConfirmation`):** `POST /confirmar/reenviar` gera novo token de 24h (aposentando links anteriores não usados via `Tokens.Create`) apenas se a conta existir em `UserStatusPendingEmail`. Para qualquer outro endereço (malformado, inexistente ou em outro status), não faz nada e retorna a mesma mensagem neutra (`confirmMsgSent`).
- **Purga automática após 7 dias (`PurgeUnconfirmedSignups`):** rotina diária (`dailyLoop`) remove contas em `UserStatusPendingEmail` criadas há mais de 7 dias, junto aos coletivos pendentes criados por elas (`Status == CollectiveStatusPending && CreatedBy == userID`), cada conta em sua própria `WithTx`. Não envia e-mail nem registra entrada de auditoria.

### 3. Estados da Conta, Login e Redirecionamento Seguro (`GET/POST /entrar`, `POST /sair`)
- **Ciclo de estados (`domain.UserStatus`):** o domínio define 4 estados válidos: `pending_email`, `pending_approval`, `active` e `disabled`. Contas recusadas na aprovação são excluídas do banco (não existe estado `rejected` armazenado), comportando-se como inexistentes no login.
- **Revelação condicional e tempo constante:** `accounts.Login` só revela o estado da conta (`ErrLoginPendingEmail`, `ErrLoginPendingApproval`, `ErrLoginDisabled`) após a verificação positiva da senha. E-mail malformado, e-mail desconhecido e conta bloqueada executam `auth.DummyVerify` para manter tempo comparável.
- **Bloqueio por tentativas erradas:** 5 senhas incorretas consecutivas (`LoginMaxFailures = 5`) bloqueiam a conta por 15 minutos (`LoginLockFor = 15 * time.Minute`). Na web, `ErrLoginFailed` e `ErrLoginLocked` exibem exatamente a mesma mensagem (`loginMsgRejected`) para não denunciar quais e-mails estão cadastrados. Login bem-sucedido em conta ativa zera `FailedLogins`.
- **Senha expirada no login:** uma senha expirada não impede a autenticação; o usuário entra normalmente e o middleware `requireFreshPassword` redireciona qualquer rota autenticada para `/senha/trocar`, liberando apenas `/senha/trocar`, `/senha/verificar` e `/sair`.
- **Proteção contra open redirect (`safeNext`):** o parâmetro `?next=` só é aceito quando começa com `/`, não começa com `//`, não contém `\` nem caracteres de controle (`< 0x20` ou `0x7f`) e faz parse sem esquema nem host; qualquer outro valor cai para `"/"`.

### 4. Sessões e Cookies (`internal/auth/session.go`, `internal/web/auth.go`)
- **Cookie:** nome `__Host-acl_session`, `HttpOnly: true`, `Secure: true` (em todos os ambientes, exigido pelo prefixo `__Host-`), `SameSite: Lax`, `Path: "/"`, sem `Domain`. Cada resposta emite no máximo um cabeçalho `Set-Cookie` para a sessão.
- **Armazenamento e rotação:** tokens têm 32 bytes aleatórios (`base64url` estrito sem padding) e apenas o hash SHA-256 (`TokenHashLen = 32`) é persistido. Todo `signIn` encerra a sessão anterior do cookie antes de criar a nova (prevenção de fixação de sessão). `User-Agent` é sanitizado para UTF-8 válido e truncado em até `UserAgentMax = 300` runas.
- **Validade deslizante e teto absoluto:** a sessão expira após **7 dias de inatividade** (`SessionIdleTimeout = 7 * 24 * time.Hour`), com teto absoluto de **30 dias a partir da criação** (`SessionMaxLifetime = 30 * 24 * time.Hour`). A atualização de `LastSeenAt` e `ExpiresAt` ocorre no máximo a cada **5 minutos** (`SessionTouchInterval = 5 * time.Minute`) e nunca ultrapassa o teto de 30 dias.
- **Revogação automática:** se o usuário não existir mais ou deixar de estar `UserStatusActive`, `loadSession` encerra a sessão no banco e limpa o cookie imediatamente.

### 5. Política de Senha, Histórico, Expiração e Lembretes (`internal/auth/password.go`, `internal/accounts/password.go`)
- **Requisitos (`auth.CheckPolicy`):** mínimo de **8 runas** (`MinLength = 8`), máximo de **128 runas** (`MaxLength = 128`), ao menos uma letra minúscula, uma letra maiúscula, um dígito e um símbolo (qualquer runa que não seja letra nem dígito, espaços incluídos), além de diferir do e-mail e da parte local do e-mail quando esta tiver 3 ou mais runas (ignorando caixa e espaços nas pontas).
- **Hash criptográfico:** `argon2id` em formato PHC estrito (`m=19456` KiB, `t=2`, `p=1`, chave 32B, salt 16B). `parsePHC` impõe limites máximos (`m <= 65536`, `t <= 10`, `p <= 4`, salt `8..64`, chave `16..64`) e comparação em tempo constante (`subtle.ConstantTimeCompare`).
- **Histórico de senhas:** impede reutilização das **2 últimas senhas** (`auth.HistorySize = 2`, mensagem `"diferente das 2 últimas senhas"`), verificado dentro da mesma transação de gravação.
- **Expiração e lembretes:** senhas expiram em **90 dias** (`auth.MaxAge = 90 * 24 * time.Hour`). Nos últimos **7 dias** (`auth.WarnBefore = 7 * 24 * time.Hour`), `viewUser` exibe o banner de aviso e o job diário `SendPasswordReminders` envia **1 e-mail** (`TemplatePasswordExpiring`) por ciclo de senha, marcando `PasswordRemindedAt` na mesma transação; `Users.ChangePassword` limpa `PasswordRemindedAt` para o próximo ciclo.
- **Troca autenticada (`POST /senha/trocar`):** valida campos antes de qualquer hash, verifica `current_password` (compartilhando o bloqueio de `BDR-001` / `ACL-43`), valida histórico, grava a nova senha e encerra todas as outras sessões mantendo apenas a atual (`auth.RevokeOtherSessions`).

### 6. Recuperação de Senha (`GET/POST /senha/esqueci`, `GET/POST /senha/redefinir`)
- **Solicitação (`RequestPasswordReset`):** envia `TemplatePasswordReset` com token `TokenPurposeResetPassword` válido por **1 hora** (`ResetTokenTTL = time.Hour`) apenas para contas `UserStatusActive`, aposentando links anteriores não usados. Para qualquer outro endereço, retorna `nil` e exibe a mesma tela de confirmação neutra.
- **Redefinição (`ResetPassword`):** `GET /senha/redefinir` apenas exibe o formulário sem consultar nem consumir o token. `POST /senha/redefinir` valida campos antes de tocar o token; falhas de validação (incluindo `RuleNotEmail` e histórico) fazem rollback e mantêm o link utilizável. O hash Argon2 da nova senha só é calculado após o consumo válido do token. Sucesso grava a nova senha, encerra **todas** as sessões da conta (`DeleteForUser(ctx, u.ID, nil)`) e limpa o cookie.

### 7. Limitação de Taxa em Memória (`Limits` em `internal/web/app.go`)
Todos os limitadores usam janela fixa em memória (`auth.Limiter`) com teto de `100.000` chaves (`LimiterMaxKeys`, modo *fail-closed* para chaves novas quando cheio). Chaves por e-mail usam o SHA-256 hexadecimal do endereço em minúsculas (`emailHash`).

| Balde (`Limits.*`) | Limite / Janela | Chave / Escopo | Rotas protegidas |
| :--- | :--- | :--- | :--- |
| `Login` | 10 / minuto | IP (`login:`, `confirm:`, `reset:`) | `POST /entrar`, `POST /confirmar`, `POST /confirmar-email`, `POST /senha/redefinir` |
| `Signup` | 5 / hora | IP (`signup:`) | `POST /cadastro` |
| `SignupEmail` | 5 / hora | Hash do e-mail (`signup-email:`) | `POST /cadastro` (após `ValidateSignup`) |
| `EmailAction` | 5 / hora | IP (`resend:`, `forgot:`, `perfil-email:`) | `POST /confirmar/reenviar`, `POST /senha/esqueci`, `POST /perfil/email` |
| `EmailActionEmail` | 5 / hora | Hash do e-mail (`*-email:`) | `POST /confirmar/reenviar`, `POST /senha/esqueci`, `POST /perfil/email` |
| `Suggestions` | 60 / minuto | IP (`suggestions:`) | `GET /coletivos/sugestoes` |
| `PasswordCheck` | 30 / minuto | Usuário (`change:`, `check:`) | `POST /senha/trocar`, `POST /senha/verificar` |
| `Requests` | 10 / hora | Usuário (`request:`) | `POST /coletivos/entrar`, `POST /coletivos/novo`, `POST /coletivo/:id/nome` |

## Exceções e Casos de Borda

- **Coletivo indisponível no cadastro:** um nome pertencente a coletivo pendente, recusado, congelado (`Locked`) ou de outro `CollectiveKind` responde com a mesma mensagem `"nome já em uso; escolha outro"`, sem revelar qual dos quatro casos ocorreu.
- **Hash corrompido ou histórico ilegível:** um hash armazenado malformado ou fora dos limites de `parsePHC` (ou falha ao ler o histórico) retorna erro de servidor (`500`), nunca falso positivo nem falha de login contabilizada.
- **Token malformado:** tokens que não tenham exatamente 32 bytes em `base64url` canônico são recusados por `auth.HashToken` antes de qualquer consulta ao banco.
- **Orçamento de e-mail protegido em `emailForm`:** se o limite por IP falhar ou o e-mail estiver vazio/inválido, o orçamento do e-mail não é consumido.
- **Sessão mantida malformada em `ChangePassword`:** se `keepSession` for vazio ou malformado, `RevokeOtherSessions` encerra todas as sessões da conta.
- **Purga sem admins ou reenvio em corrida:** `ConfirmEmail` sem admins ativos no banco conclui normalmente sem enfileirar e-mails; `PurgeUnconfirmedSignups` interrompe com erro e faz rollback se um vínculo apontar para coletivo inexistente (invariante quebrada).

## Prova Executável (BDD)

- **Domínio e validação:**
  - `internal/domain/model_test.go`: `TestUserStatusValid`, `TestTokenPurposeValid`, `TestEnumValuesAreStable`
  - `internal/domain/rules_test.go`: `TestUserValidate`, `TestTokenValidate`, `TestSessionValidate`, `TestNormalizeEmail`, `TestTextProblem`
- **Política de senha, tokens, sessões e rate limit:**
  - `internal/auth/password_test.go`: `TestPolicyConstants`, `TestRulesOrder`, `TestRuleLabels`, `TestCheckPolicy`, `TestHashPasswordFormat`, `TestVerifyPassword`, `TestVerifyPasswordRejectsBadHashes`, `TestMatchesAny`, `TestDummyVerifyDoesTheWorkOfAVerify`, `TestPasswordExpired`, `TestDaysUntilExpiry`, `TestExpiryWarning`
  - `internal/auth/session_test.go`: `TestSessionConstants`, `TestStartSessionStoresOnlyTheHash`, `TestStartSessionCleansTheUserAgent`, `TestResolveSessionIdleExpiry`, `TestResolveSessionMaxLifetimeEvenWhenStoredExpiryIsLater`, `TestResolveSessionWritesAtMostEveryFiveMinutes`, `TestResolveSessionSlideNeverPassesThirtyDays`, `TestRevokeOtherSessionsKeepsExactlyOne`, `TestMalformedCookieValueNeverReachesTheStore`
  - `internal/auth/token_test.go`: `TestNewTokenRoundTrip`, `TestNewTokenDistinct`, `TestHashTokenRejectsMalformed`
  - `internal/auth/limiter_test.go`: `TestLimiterStopsAtTheLimit`, `TestLimiterWindowStartsAtTheFirstHitAndDoesNotSlide`, `TestLimiterSweepsEndedWindows`, `TestLimiterFailsClosedForNewKeysAtTheCap`
- **Serviços de conta (`internal/accounts`):**
  - `internal/accounts/signup_test.go`: `TestSignupNewCollectiveAsOwner`, `TestSignupJoinsApprovedCollectiveByID`, `TestSignupTypedNameJoinsApprovedCollective`, `TestSignupCollectiveErrorsDoNotDependOnTheEmail`, `TestSignupExistingEmail`, `TestSignupEmailTakenRace`, `TestSignupSerializesWithAFreeze`, `TestValidateSignup`
  - `internal/accounts/confirm_test.go`: `TestConfirmEmail`, `TestConfirmEmailMalformedTokenNeverReachesTheStore`, `TestConfirmEmailRollsBackWhenTheAccountIsNotPendingEmail`, `TestResendConfirmationIssuesANewLink`, `TestResendConfirmationTokenLifetime`, `TestResendConfirmationSaysNothingForOtherAccounts`
  - `internal/accounts/login_test.go`: `TestLoginActiveUser`, `TestLoginNormalizesTheEmail`, `TestLoginUnknownOrMalformedEmail`, `TestLoginLocksAfterFiveWrongPasswords`, `TestLoginRevealsStatusOnlyWithTheRightPassword`, `TestLoginMalformedStoredHashIsAServerError`
  - `internal/accounts/password_test.go`: `TestChangePasswordSetsThePasswordAndEndsTheOtherSessions`, `TestChangePasswordRefusesAPasswordUsedLately`, `TestChangePasswordMalformedKeepSessionKeepsNone`, `TestSendPasswordRemindersMailsOneNoticeForAnExpiringPassword`, `TestSendPasswordRemindersSkipsUsersAlreadyRemindedThisCycle`, `TestSendPasswordRemindersSendsAgainAfterChangePassword`, `TestSendPasswordRemindersIgnoresPasswordsOutsideTheWarningWindow`
  - `internal/accounts/reset_test.go`: `TestRequestPasswordResetMailsALinkThatResetsThePassword`, `TestResetLinkLivesForOneHour`, `TestRequestPasswordResetIgnoresEveryOtherAddress`, `TestRequestPasswordResetRetiresTheEarlierLink`, `TestResetPasswordFieldErrorsLeaveTheLinkUsable`, `TestResetPasswordRefusesAnAccountThatIsNotActive`, `TestResetPasswordEndsEverySessionAndUnlocksTheAccount`, `TestResetPasswordHashesOnlyForAWorkingLink`
  - `internal/accounts/profile_test.go`: `TestPurgeUnconfirmedSignups`
- **Rotas HTTP e middleware (`internal/web`):**
  - `internal/web/auth_test.go`: `TestLoadSession`, `TestRequireAuth`, `TestRequireFreshPassword`, `TestSignIn`, `TestSignOut`, `TestSafeNext`, `TestNewAppLimits`
  - `internal/web/routes_signup_test.go`: `TestSignupCreatesAnAccount`, `TestSignupWithARegisteredEmailLooksTheSame`, `TestSignupRateLimitPerIP`, `TestSignupRateLimitPerEmail`, `TestSignupInvalidPostsDoNotSpendEmailBudget`, `TestSignupIPAndEmailLimitersAreIsolated`
  - `internal/web/routes_confirm_test.go`: `TestConfirmGetShowsTheButtonWithoutTouchingTheStore`, `TestConfirmGetDoesNotConsumeTheToken`, `TestConfirmPostConfirmsTheAccountAndNotifiesTheAdmins`, `TestConfirmResendAnswersTheSameForEveryAddress`, `TestConfirmResendRefusedIPDoesNotSpendTheEmailBudget`
  - `internal/web/routes_login_test.go`: `TestLoginSuccess`, `TestLoginSuccessGoesToASafeNextOnly`, `TestLoginSuccessRotatesTheSession`, `TestLoginWithAnExpiredPasswordStillSignsIn`, `TestLoginDoesNotRevealTheStatusWithoutThePassword`, `TestLoginLocksTheAccountAfterFiveWrongPasswords`, `TestLoginAnswersTheSameForARegisteredAndAnUnknownEmail`, `TestLoginRateLimit`, `TestLogoutEndsTheSession`
  - `internal/web/routes_password_test.go`: `TestPasswordChangeSetsThePasswordAndEndsTheOtherSessions`, `TestPasswordChangeLetsAnExpiredPasswordThrough`, `TestPasswordChangeIsLimitedPerAccount`, `TestPasswordVerifyIsLimitedPerAccount`
  - `internal/web/routes_reset_test.go`: `TestResetForgotAnswersTheSameForEveryAddress`, `TestResetGetDoesNotConsumeTheToken`, `TestResetPostChangesThePasswordAndEndsEverySession`, `TestResetPostFieldErrorsKeepTheLinkUsable`, `TestResetFlowKeepsTheTokenTheAddressAndThePasswordOutOfTheLogs`
- **Cobertura BDD / UAT:** `@BDD-01-01` a `@BDD-01-18`, `UAT-S1` a `UAT-S4`.
