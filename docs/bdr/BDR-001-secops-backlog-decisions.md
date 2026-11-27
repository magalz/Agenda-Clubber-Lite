---
id: BDR-001
title: "Decisões de Produto e Segurança de Domínio do Backlog SecOps"
status: accepted
date: 2026-11-26
superseded_by: null
issues: [ACL-39, ACL-43, ACL-48]
scope: [internal/accounts, internal/web, internal/mail]
---

# BDR-001: Decisões de Produto e Segurança de Domínio do Backlog SecOps

## Contexto de Negócio

Durante a triagem do backlog SecOps do Agenda Clubber Lite (calendário comunitário fechado, com aprovação manual pelo Admin para coletivos e produtoras de festas), três achados exigiram definições explícitas de produto equilibrando mitigação de abuso e experiência de uso:

1. **ACL-39 — Enumeração em duas etapas de nomes de coletivos no cadastro:** criar um novo coletivo durante o cadastro valida a unicidade do nome do coletivo (`domain.NameKey`), o que pode revelar em duas etapas se determinado nome de coletivo já existe (mesmo quando pendente, recusado, congelado ou de outro tipo).
2. **ACL-43 — Força bruta em verificações autenticadas de senha:** a troca de senha autenticada (`POST /senha/trocar`) e a verificação ao vivo de histórico de senha (`POST /senha/verificar`) anteriormente não compartilhavam o contador de bloqueio de login ao receber `current_password` incorreta.
3. **ACL-48 — Nome não confiável em e-mails de confirmação de cadastro:** os e-mails de confirmação enviados a endereços ainda não confirmados (`verify_email.tmpl`) anteriormente interpolavam o campo `Name` informado no formulário, permitindo que um atacante enviasse mensagens com texto ou URLs de phishing para endereços de terceiros.

## Regra Acordada

1. **ACL-39 (Risco Aceito — Sem Alteração de Código):** o produto aceita o risco de baixo valor de enumeração em duas etapas de nomes de coletivos no cadastro. O Agenda Clubber Lite é um calendário comunitário fechado, o cadastro já é restringido pelo limite de 5 tentativas por hora por IP (`Limits.Signup`) e contas não confirmadas (junto aos coletivos pendentes criados por elas) são purgadas automaticamente após 7 dias (`PurgeUnconfirmedSignups`). Nenhuma alteração de código é aplicada.
2. **ACL-43 (Bloqueio de Conta Compartilhado):** o login (`POST /entrar`), a troca de senha (`POST /senha/trocar`) e a verificação de histórico (`POST /senha/verificar`) compartilham o mesmo bloqueio de conta de 5 tentativas / 15 minutos (`LoginMaxFailures = 5`, `LoginLockFor = 15 * time.Minute`, campos `FailedLogins` e `LockedUntil`) sempre que uma `current_password` não vazia estiver incorreta. Uma `current_password` válida zera `FailedLogins` mesmo se a nova senha for recusada posteriormente (por exemplo, por reutilização do histórico). Enquanto a conta estiver bloqueada, a verificação Argon2 é ignorada. O produto aceita explicitamente o risco de autobloqueio em caso de erros repetidos de digitação.
3. **ACL-48 (Saudação Sem Nome para Endereços Não Confirmados):** o template `verify_email.tmpl` (usado na confirmação de cadastro e no reenvio de confirmação) utiliza saudação fixa sem nome (`"Olá!"`) e omite o `Name` enviado no formulário da struct `VerifyEmailData`, impedindo que texto ou URLs fornecidos por terceiros apareçam em e-mails enviados a endereços não confirmados. O template `existing_account.tmpl` continua utilizando o nome já armazenado na conta.

## Exceções e Casos de Borda

- **ACL-43 com `current_password` vazia ou longa demais:** `current_password` vazia ou erros de validação de campo em `POST /senha/trocar`, assim como entradas vazias ou acima de `auth.MaxLength` em `POST /senha/verificar`, não registram falha de login nem executam Argon2.
- **ACL-48 em contas já verificadas:** apenas os e-mails de verificação para endereços não confirmados (`verify_email.tmpl`) omitem o nome; e-mails enviados a contas já registradas (`existing_account.tmpl`, e-mails de decisão, lembretes de expiração de senha e avisos de troca de e-mail) preservam o nome armazenado da conta.

## Prova Executável (BDD)

- **ACL-39:** sem alteração de código; mitigações vigentes cobertas por:
  - `internal/web/routes_signup_test.go`: `TestSignupRateLimitPerIP`, `TestSignupSuggestionsRateLimit`
  - `internal/accounts/profile_test.go`: `TestPurgeUnconfirmedSignups`
- **ACL-43:**
  - `internal/accounts/password_test.go`: `TestChangePasswordLocksAfterRepeatedWrongCurrentPasswords`, `TestChangePasswordLockedSkipsArgon2`, `TestChangePasswordValidCurrentResetsFailedLoginsEvenOnHistoryRefusal`, `TestChangePasswordFieldErrorsRecordNoFailures`, `TestCheckPasswordHistoryLocksAndSkipsArgon2WhenLocked`, `TestCheckPasswordHistoryValidCurrentResetsFailedLogins`, `TestCheckPasswordHistorySkipsStoreAndArgon2OnEmptyOrOverlong`
  - `internal/web/routes_password_test.go`: `TestPasswordChangeLocksAfterFiveWrongCurrentPasswords`, `TestPasswordVerifyLocksAndResetsFailures`
- **ACL-48:**
  - `internal/accounts/signup_test.go`: `TestSignupVerifyEmailOmitsSubmittedName`
  - `internal/accounts/confirm_test.go`: `TestResendConfirmationOmitsSubmittedName`
  - `internal/mail/render_test.go`: `TestRenderEscapesUserText`, `TestRenderRejectsBadInput`
- **Cobertura BDD / UAT:** `ACL-43`: `@BDD-01-14`, `UAT-S4-05`, `UAT-S4-06`; `ACL-48`: `@BDD-01-01`, `@BDD-01-04`, `@BDD-01-08`, `UAT-S1-03`, `UAT-S2-04`, `UAT-S10-02`; `ACL-39`: `@BDD-01-05`, `@BDD-01-09`, `UAT-S1-08`, `UAT-S10-04`.
- **Rastreabilidade:** `ACL-39` (risco aceito, sem commit de código), `ACL-43` (commit `3ac0385`), `ACL-48` (commit `6db5ca5`).
