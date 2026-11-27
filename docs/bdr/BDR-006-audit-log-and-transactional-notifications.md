---
id: BDR-006
title: "Trilha de Auditoria e Notificações Transacionais"
status: accepted
date: 2026-11-26
superseded_by: null
issues: [ACL-32, ACL-37, ACL-38, ACL-48, ACL-126, ACL-127]
scope: [internal/domain, internal/approvals, internal/events, internal/accounts, internal/mail, cmd/agenda]
---

# BDR-006: Trilha de Auditoria e Notificações Transacionais

## Contexto de Negócio
Como o Agenda Clubber Lite opera com aprovações administrativas, gestão delegada por coletivos e comunicação por e-mail para fluxos críticos de conta e governança, duas garantias operacionais são indispensáveis:

1. **Rastreabilidade sem vazamento de dados pessoais (PII):** o Admin precisa auditar decisões de contas, coletivos, vínculos e intervenções em eventos em `/admin/historico`, sem armazenar endereços de e-mail na trilha e sem perder legibilidade quando uma conta ou coletivo é removido posteriormente.
2. **Entrega confiável e segura de e-mails transacionais:** e-mails são enfileirados dentro da mesma transação de banco (`Outbox`) que confirma a operação de negócio, entregues de forma assíncrona com retentativa controlada, proteção contra reenvio duplicado em falhas de encerramento SMTP (`QUIT`) e redação rigorosa de credenciais e destinatários nos logs e mensagens de erro.

Para a decisão específica sobre omissão do nome submetido no cadastro em `verify_email.tmpl` (`ACL-48`), consulte também o [BDR-001](./BDR-001-secops-backlog-decisions.md).

## Regra Acordada

### 1. Matriz Completa da Trilha de Auditoria (`domain.AuditAction`)
A trilha de auditoria (`AuditEntry`) registra exclusivamente as **17 ações** definidas em `internal/domain/model.go`, sempre gravadas dentro da mesma `Store.WithTx` da operação:

| Constante Go | Valor armazenado | Origem / Fluxo |
|---|---|---|
| `AuditAccountApproved` | `account_approved` | Admin aprova conta pendente (`internal/approvals/accounts.go`) |
| `AuditAccountRejected` | `account_rejected` | Admin recusa conta pendente (`internal/approvals/accounts.go`) |
| `AuditAccountBlocked` | `account_blocked` | Admin bloqueia/desativa conta ativa (`internal/approvals/accounts.go`) |
| `AuditAccountUnblocked` | `account_unblocked` | Admin desbloqueia/reativa conta (`internal/approvals/accounts.go`) |
| `AuditCollectiveRequested` | `collective_requested` | Usuário solicita criação de novo coletivo (`internal/approvals/requests.go`) |
| `AuditCollectiveApproved` | `collective_approved` | Admin aprova coletivo pendente (`internal/approvals/collectives.go`) |
| `AuditCollectiveRejected` | `collective_rejected` | Admin recusa coletivo pendente (`internal/approvals/collectives.go`) |
| `AuditCollectiveRenamed` | `collective_renamed` | Administrador renomeia coletivo (`internal/approvals/requests.go`) |
| `AuditCollectiveBlocks` | `collective_blocks_changed` | Admin altera `EventsBlocked` ou `Locked` do coletivo (`internal/approvals/collectives.go`) |
| `AuditJoinRequested` | `join_requested` | Usuário solicita entrada em coletivo (`internal/approvals/requests.go`) |
| `AuditJoinAdminApproved` | `join_admin_approved` | Admin aprova pedido de entrada (`internal/approvals/joins.go`) |
| `AuditJoinOwnerApproved` | `join_owner_approved` | Administrador do coletivo aprova pedido de entrada (`internal/approvals/joins.go`) |
| `AuditJoinRejected` | `join_rejected` | Admin ou Administrador recusa pedido de entrada (`internal/approvals/joins.go`) |
| `AuditMemberPromoted` | `member_promoted` | Promoção de Membro para Administrador (`internal/approvals/joins.go`) |
| `AuditMemberDemoted` | `member_demoted` | Rebaixamento de Administrador para Membro (`internal/approvals/joins.go`) |
| `AuditEventEdited` | `event_edited` | Admin edita evento de coletivo no qual **não** é Administrador aprovado (`internal/events/write.go`) |
| `AuditEventDeleted` | `event_deleted` | **Toda** exclusão de evento, por Administrador ou pelo Admin (`internal/events/write.go`) |

**Regras de snapshot, retenção e exclusões explícitas:**
- **O que NÃO é auditado:** criação de eventos; edição de evento feita por um Administrador aprovado do próprio coletivo (mesmo que esse ator também seja Admin do site); alterações de autoatendimento em perfil ou e-mail (`/perfil`, `/perfil/email`, `/confirmar-email`); expurgo automático de cadastros não confirmados (`PurgeUnconfirmedSignups`).
- **Snapshots sem e-mail:** cada `AuditEntry` guarda IDs e snapshots de nomes (`ActorName`, `UserName`, `CollectiveName`), **nunca** um endereço de e-mail. As referências não são checadas na escrita e nunca são apagadas ou modificadas se o usuário ou coletivo for excluído depois.
- **Formato de detalhe em eventos:** `Detail` para `event_edited` e `event_deleted` segue estritamente `<Nome do Evento> · <dd/mm/aaaa>`, com a data de início formatada em `APP_TZ` (`events.DateLayout`).
- **Limite de visualização:** `/admin/historico` exibe os **200** registros mais recentes (ordenados por `At` decrescente, desempate por `ID` decrescente); `Detail` e `Reason` têm teto de `domain.AuditTextMax = 500` caracteres.

### 2. Catálogo dos 10 Templates de E-mail Transacional (`internal/mail/render.go`)
Todos os e-mails são renderizados em `multipart/alternative` (texto plano + HTML escapado por `html/template`, assunto em linha única, sem `User-Agent` padrão e com `Message-ID` aleatório no domínio do remetente):

| Constante Go | Template (`.tmpl`) | Destinatário e finalidade |
|---|---|---|
| `TemplateVerifyEmail` | `verify_email` | Novo endereço no cadastro ou reenvio de confirmação (link válido por 24h; saudação sem nome conforme `ACL-48` / [BDR-001](./BDR-001-secops-backlog-decisions.md)). |
| `TemplateExistingAccount` | `existing_account` | Titular de conta já existente quando alguém tenta cadastrar ou trocar e-mail para aquele endereço (links de login e `/senha/esqueci`, nunca link de reset). |
| `TemplateAdminNewSignup` | `admin_new_signup` | Admins ativos após confirmação de e-mail de um novo cadastro (nome, e-mail, telefone/WhatsApp e lista de coletivos solicitados). |
| `TemplatePasswordReset` | `password_reset` | Titular da conta ao solicitar redefinição de senha (link válido por 1h). |
| `TemplateDecision` | `decision` | Usuário afetado por decisões de aprovação/recusa de conta, coletivo ou vínculo (um único e-mail consolidado por ação). |
| `TemplateOwnerJoinRequest` | `owner_join_request` | Administradores ativos de um coletivo quando um pedido de entrada fica visível para aprovação deles. |
| `TemplateAdminRequest` | `admin_request` | Admins ativos quando há solicitação de entrada (`join`), novo coletivo (`new_collective`) ou renomeação (`rename`). |
| `TemplateChangeEmail` | `change_email` | Novo endereço informado em `POST /perfil/email` (link de confirmação válido por 24h). |
| `TemplateEmailChangeNotice` | `email_change_notice` | Endereço atual (antigo) avisando que uma troca para `NewEmail` está pendente; nenhuma segunda mensagem é enviada após confirmar. |
| `TemplatePasswordExpiring` | `password_expiring` | Conta ativa dentro da janela de aviso de 7 dias (`auth.WarnBefore`) antes da expiração da senha (1 lembrete por ciclo de senha). |

**Regra do botão CTA em `decision.tmpl`:**
- `DecisionData.Button` aceita exatamente três valores (qualquer outro falha via `fail` na renderização): `"Entrar"`, `"Ver meus coletivos"` ou `"Criar conta"`, escolhidos conforme a página de destino (`Link`).
- A nota condicional *"se sua conta ainda aguarda aprovação, o link começa a funcionar depois que ela for aprovada."* é exibida **somente** quando `Button == "Entrar"`.

### 3. Contrato do Outbox, Leases e Política de Retentativa (`internal/mail/worker.go`)
- **Enfileiramento transacional:** mensagens são gravadas em `Outbox.Enqueue` dentro da mesma transação de negócio. A entrega é **at-least-once**.
- **Claim atômico e guarda de lease (`ACL-37`):** `Outbox.Claim(ctx, now, lease, limit)` seleciona mensagens não enviadas, não desistidas (`GaveUp == false`), vencidas (`NextAttemptAt <= now`) e sem lease ativo (`ClaimedUntil` ausente ou `<= now`), ordenadas por `NextAttemptAt` ascendente, aplicando `ClaimedUntil = now + lease` atomicamente. Se `limit < 1` ou `lease <= 0`, retorna vazio e não reivindica nenhuma mensagem.
- **Prazos do worker:** intervalo padrão de polling `5s` (`defaultInterval`), `Lease` padrão de `5m` (`defaultLease`), `SendTimeout` padrão de `3m` (`defaultSendTimeout`) e `markTimeout` de `10s` (usando `context.WithoutCancel(ctx)` para não perder o registro de envio durante shutdown). O worker reivindica 1 mensagem por vez.
- **Agenda de retentativas (`RetryDelays` / `MaxAttempts`):** no máximo **`6` tentativas** (`MaxAttempts = 6`). Após falhas 1 a 5, o backoff é **`1m` → `5m` → `30m` → `2h` → `6h`**. Na 6ª falha, `retryAt` é `nil` e `MarkFailed` marca `GaveUp = true`, encerrando futuras tentativas. `LastError` é truncado a `domain.OutboxErrorMax = 500` caracteres.

### 4. Comportamento SMTP Pós-Entrega e Modos de Desenvolvimento
- **Falha no `QUIT` após entrega (`ACL-38` / `ACL-32`):** em `SMTPSender.Send`, o envio (`SendWithSMTPClient`) e o fechamento (`CloseWithSMTPClient`) são executados separadamente. Se `SendWithSMTPClient` for bem-sucedido e `CloseWithSMTPClient` falhar, o remetente registra um aviso (`log.Warn("mail: smtp close failed after delivery", ...)`) e **retorna `nil`**, evitando que o outbox reenvie um e-mail já entregue. Se `SendWithSMTPClient` falhar, o erro de envio nunca é mascarado por `closeErr`.
- **TLS, autenticação e redação:** com `SMTP_USERNAME` configurado, TLS (`STARTTLS` na porta 587 ou TLS implícito na 465) e `AUTH PLAIN` são obrigatórios; sem `SMTP_USERNAME` (ex.: Mailpit em dev), envia sem TLS/auth. Erros SMTP são sanitizados em linha única, removendo credenciais (`newRedactor`) e o endereço do destinatário (`redactAddress`) antes de ir para logs ou `LastError`.
- **Seleção de remetente em `dev` vs `prod` (`cmd/agenda/main.go`):**
  - `MAIL_DROP_DIR` é aceito apenas em `APP_ENV=dev` (recusado em `prod`) e **tem precedência sobre `SMTP_HOST`**, gravando arquivos `.eml` atomicamente (arquivo temporário `0600` + rename) com `From = SMTP_FROM`.
  - Em `dev` sem `MAIL_DROP_DIR` e sem `SMTP_HOST`, `serve` registra **um único aviso** (`"email sending disabled: no SMTP_HOST or MAIL_DROP_DIR"`), não inicia o worker do outbox e mantém as mensagens enfileiradas na coleção.
  - Em `prod`, `SMTP_HOST` e `SMTP_USERNAME` (com `SMTP_PASSWORD`) são obrigatórios na inicialização.

## Exceções e Casos de Borda
- **Falha ao gravar `MarkSent` após envio bem-sucedido:** o worker contabiliza falha e registra erro de log; a mensagem permanece no outbox e será reenviada quando o lease expirar (semântica *at-least-once*).
- **Falha ao gravar `MarkFailed`:** o worker não registra veredito de retry/desistência; a mensagem volta à fila ao vencer o lease.
- **Cancelamento de contexto durante envio:** o registro do resultado (`MarkSent` ou `MarkFailed`) roda sob `context.WithoutCancel(ctx)` limitado a `10s`, evitando reenvios desnecessários no desligamento gracioso (`startJobs` aguarda a mensagem em voo).
- **Assunto com quebra de linha ou destinatário inválido:** `Render` normaliza quebras de linha no assunto para linha única e `buildMessage` recusa destinatário inválido, assunto multilinha ou corpo texto vazio sem ecoar os dados da mensagem.
- **Ausência de admins ativos:** `ConfirmEmail` sem nenhum admin ativo não enfileira `TemplateAdminNewSignup`.

## Prova Executável (BDD)
- **Auditoria em eventos, aprovações e exclusões (`internal/events`, `internal/domain`, `internal/accounts`, `internal/web`):**
  - `internal/events/write_test.go`: `TestWriteAudit`
  - `internal/domain/model_test.go`: `TestAuditActionValid`, `TestEnumValuesAreStable`
  - `internal/domain/rules_test.go`: `TestAuditEntryValidate`
  - `internal/accounts/profile_test.go`: `TestPurgeUnconfirmedSignups`
  - `internal/web/routes_admin_manage_test.go`: `TestMngHistoryEmpty`, `TestMngHistoryShowsTheDecisionsNewestFirst`, `TestMngHistoryLabelsEveryAction`, `TestMngAuditActionLabel`, `TestMngHistoryShowsTheLatest200`
  - `internal/store/mongostore/mongostore_test.go`: `TestStore` (`storetest.RunAuditEntries`, `storetest.RunOutbox`), `TestSchemaChecksAuditEntries`, `TestSchemaAcceptsTheEventAuditActions`
- **Templates de e-mail e `decision.tmpl` (`internal/mail`):**
  - `internal/mail/render_test.go`: `TestRender`, `TestRenderDecisionKeepsTheLinkInTextForEveryDestination`, `TestRenderPasswordExpiringHasButtonAndCopyLinkFallback`, `TestRenderChangeEmailKeepsTheLinkInPlace`, `TestRenderEmailChangeNoticeHasNoActionLink`, `TestRenderEscapesUserText`, `TestRenderSubjectStaysOnOneLine`, `TestRenderDecisionKeepsEachReasonWithItsItem`, `TestRenderDecisionReasonKeepsLineBreaks`, `TestRenderRejectsBadInput`, `TestRenderDecisionRejectsUnknownButton`, `TestDecisionButtonsFollowTheirDestinations`
- **Worker do outbox, retentativas, SMTP `QUIT` e redação (`internal/mail`, `cmd/agenda`):**
  - `internal/mail/worker_test.go`: `TestWorkerSendsADueMessageOnce`, `TestWorkerSkipsALeasedMessageUntilItsLeaseRunsOut`, `TestWorkerRetrySchedule`, `TestWorkerRecordsAFailureWhenCanceledMidSend`, `TestWorkerRecordsASentMessageWhenCanceledMidSend`, `TestWorkerBoundsEachSend`, `TestWorkerLeaseDefaultsToFiveMinutes`, `TestWorkerLogsNeverHoldTheMessage`, `TestWorkerWritesNoVerdictWhenMarkFailedFails`, `TestWorkerKeepsTheRecipientOutOfLogAndLastErrorWhenTheServerRefusesIt`, `TestWorkerSendsAgainWhenMarkSentFails`
  - `internal/mail/smtp_test.go`: `TestSMTPSenderDeliversWithoutAuthOrTLS`, `TestSMTPSenderAuthenticatesOverSTARTTLS`, `TestSMTPSenderWithUsernameNeedsSTARTTLS`, `TestSMTPSenderRedactsCredentialsFromErrors`, `TestSMTPSenderKeepsTheRecipientOutOfErrors`, `TestSMTPSenderSucceedsWhenQuitFails`, `TestSMTPSenderDoesNotMaskSendErrorWithCloseError`, `TestSMTPSenderClosesConnectionOnHandshakeFailure`
  - `internal/mail/drop_test.go`: `TestDropSenderWritesAParsableMessage`, `TestDropSenderWritesOneFilePerSend`, `TestDropSenderUsesItsFromAddress`, `TestNewDropSenderRefusesAnUnusableFolder`
  - `internal/mail/mail_test.go`: `TestSendersRefuseUnsafeMessages`
  - `cmd/agenda/main_test.go`: `TestNewSender`, `TestRunServeDeliversQueuedMailThroughTheDropFolder`, `TestRunServeWithoutASenderWarnsOnceAndLeavesTheOutbox`, `TestStartJobsStopWaitsForTheMessageInFlight`
- **Cobertura BDD / UAT:** `@BDD-05-01` a `@BDD-05-10`, `UAT-S10-01` a `UAT-S10-05`.
