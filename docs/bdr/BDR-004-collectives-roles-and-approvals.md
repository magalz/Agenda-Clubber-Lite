---
id: BDR-004
title: "Coletivos, Papéis, Governança e Fluxos de Aprovação"
status: accepted
date: 2026-11-26
superseded_by: null
issues: [ACL-127]
scope: [internal/approvals, internal/domain, internal/web]
---

# BDR-004: Coletivos, Papéis, Governança e Fluxos de Aprovação

## Contexto de Negócio

O Agenda Clubber Lite organiza o acesso e a autoria de eventos em torno de coletivos e produtoras. Para evitar cadastros indevidos, tomadas hostis de coletivos ou perda de controle administrativo, o produto separa a curadoria global do site (Admin) da gestão interna de cada coletivo (Administradores e Master), aplicando aprovação dupla para novos integrantes, proteção contra remoção do último responsável, controles administrativos de bloqueio/congelamento e isolamento rigoroso da página de gestão de cada coletivo.

## Regra Acordada

### 1. Papéis, Rótulos e Tipos de Entidade
- **Papéis de pessoa e coletivo:**
  - **Admin** (`User.IsAdmin`): administrador global do site; decide cadastros, novos coletivos, lado admin de pedidos de entrada, bloqueio de contas e bloqueios de coletivos.
  - **Master** (`Collective.CreatedBy`): criador do coletivo; exibido com o rótulo `"Master"` (`roleLabelMaster`), é um Administrador permanente e **nunca pode ser rebaixado**.
  - **Administrador** (valor armazenado `domain.RoleOwner` = `"owner"`): gere membros, pedidos de entrada, nome do coletivo e eventos do coletivo.
  - **Membro** (valor armazenado `domain.RoleMember` = `"member"`): integra o coletivo sem poder gerir membros ou publicar eventos.
  - Os valores armazenados nunca mudam; `approvals.RoleLabel` traduz `owner` → `"Administrador"` e `member` → `"Membro"`.
- **Tipos (`domain.CollectiveKind`):** `coletivo` (`"Coletivo"`) e `produtora` (`"Produtora"`).

### 2. Normalização e Unicidade de Nomes (`domain.NameKey`, `domain.CleanCollectiveName`)
- Todo nome de coletivo informado em cadastro, pedido de novo coletivo ou renomeação passa por `domain.CleanCollectiveName`: `strings.TrimSpace`, `2`–`80` runas (`CollectiveNameMin`..`CollectiveNameMax`), linha única sem caracteres de controle e `NameKey` não vazio.
- `domain.NameKey` gera a chave única de comparação: converte para minúsculas, remove diacríticos portugueses (`áàâãä`, `éèêë`, `íìîï`, `óòôõö`, `úùûü`, `ç`, `ñ`) e marcas combinantes Unicode (`U+0300`..`U+036F`) e colapsa sequências de espaços em um único espaço. Dois nomes que diferem apenas em maiúsculas, acentos ou espaçamento compartilham a mesma `NameKey` e são considerados iguais para unicidade.

### 3. Fluxos de Aprovação Dupla e Fila do Admin (`/admin`)
- **Conta primeiro:** o Admin não pode aprovar isoladamente um coletivo pendente (`ApproveCollective`) nem o lado admin de um pedido de entrada (`AdminApproveJoin`) enquanto a conta do solicitante não estiver `UserStatusActive` (retorna `ErrNotAllowed`).
- **Aprovar só a conta (`ApproveAccount`, `POST /admin/conta/aprovar`):** muda a conta de `pending_approval` para `active`, grava auditoria `AuditAccountApproved`, envia e-mail de decisão com botão `"Entrar"` e notifica os Administradores de cada coletivo aprovado onde a pessoa tem pedido pendente (que só então fica visível em `/coletivo/:id`).
- **Aprovar tudo (`ApprovePerson`, `POST /admin/pessoa/aprovar-tudo`):** em uma única `WithTx`, aprova a conta (se `pending_approval`), aprova cada coletivo pendente criado pela própria pessoa e aprova o lado admin de cada pedido de entrada pendente em coletivos aprovados. Emite **um único e-mail consolidado** (botão `"Ver meus coletivos"`) e uma entrada de auditoria por decisão. Coletivos pendentes criados por terceiros e itens já decididos são ignorados; se nada estiver aberto para decidir, retorna `ErrDecided`.
- **Aprovação de coletivo novo (`ApproveCollective`, `POST /admin/coletivo/aprovar`):** requer apenas a aprovação do Admin, pois o vínculo do criador já possui `OwnerApprovedBy/At` preenchidos desde o cadastro ou `RequestCollective`. Aprovar o coletivo aprova o vínculo do criador na mesma transação.
- **Aprovação dupla de pedido de entrada (`AdminApproveJoin` + `OwnerApproveJoin`):** um pedido em coletivo aprovado exige a aprovação do Admin **e** de um Administrador aprovado do coletivo, **em qualquer ordem**:
  - Se um **Administrador** aprova primeiro (`OwnerApproveJoin`), registra `OwnerApprovedBy/At` e audita `AuditJoinOwnerApproved`, mas **não envia e-mail** ao solicitante (pois ainda depende do Admin).
  - Se o **Admin** aprova primeiro (`AdminApproveJoin`), registra `AdminApprovedBy/At`, audita `AuditJoinAdminApproved` e envia e-mail de decisão informando que o pedido aguarda um Administrador (`mail.DecisionWaiting`).
  - A segunda aprovação (de qualquer lado) define `MembershipStatusApproved` e `DecidedAt` e envia o e-mail de decisão aprovada (`mail.DecisionApproved`).
- **Rejeições e exclusão em cascata:**
  - `RejectAccount`: exclui a conta (`Users.Delete`, em cascata sobre sessões, tokens e vínculos) e exclui os coletivos pendentes criados pela pessoa (preservando coletivos pendentes de terceiros). Audita a conta (`AuditAccountRejected`) e cada coletivo removido (`AuditCollectiveRejected`) e envia 1 e-mail consolidado com botão `"Criar conta"` para que a pessoa possa se recadastrar.
  - `RejectCollective`: exclui o coletivo pendente e seus vínculos, audita `AuditCollectiveRejected` e envia e-mail ao criador (botão `"Entrar"`).
  - `RejectJoin` (pelo Admin ou por Administrador aprovado do coletivo): exclui o vínculo pendente para permitir novo pedido futuro, audita `AuditJoinRejected` e envia e-mail ao solicitante (botão `"Ver meus coletivos"`).
  - Motivos opcionais de recusa ou bloqueio passam por `approvals.CheckReason`: normaliza `\r\n` e `\r` para `\n` e valida via `domain.TextProblem(..., 0, domain.AuditTextMax, true)` (até `500` runas, multilinha).
- **Solicitações autenticadas (`/coletivos`, `Limits.Requests` = 10/h por usuário):**
  - `RequestJoin` (`POST /coletivos/entrar`): aceita coletivo aprovado e não congelado; impede pedido duplicado se já houver vínculo em qualquer status (`msgAlreadyIn`); notifica Administradores e Admins e audita `AuditJoinRequested`. Sem JavaScript, aceita nome digitado resolvido por `NameKey` apenas para coletivo já aprovado.
  - `RequestCollective` (`POST /coletivos/novo`): cria coletivo `pending` com criador como Master/`RoleOwner`, notifica Admins e audita `AuditCollectiveRequested`.

### 4. Governança Interna de Membros e Renomeação (`/coletivo/:id`)
- **Promoção (`Promote`, `POST /coletivo/:id/membro/promover`):** um Administrador aprovado promove um Membro aprovado cujo usuário esteja **ativo** (`UserStatusActive`; caso contrário `ErrNotAllowed`). Audita `AuditMemberPromoted` e não envia e-mail.
- **Rebaixamento (`Demote`, `POST /coletivo/:id/membro/rebaixar`):** um Administrador aprovado rebaixa outro Administrador (ou a si mesmo) para Membro, sujeito a duas guardas invioláveis:
  - **Imutabilidade do Master:** se o alvo for `Collective.CreatedBy`, retorna `ErrMaster` (`409`).
  - **Guarda do último Administrador ativo:** exige ao menos outro `RoleOwner` com `MembershipStatusApproved` e `UserStatusActive` (senão retorna `ErrLastOwner`, `409`).
  - **Autorrebaixamento:** permitido quando respeita as duas guardas acima; na web, redireciona para `/coletivos?msg=voce-rebaixado` (pois a pessoa perde acesso a `/coletivo/:id`). Audita `AuditMemberDemoted` e não envia e-mail.
- **Renomeação imediata (`RenameCollective`, `POST /coletivo/:id/nome`):** um Administrador aprovado renomeia coletivo aprovado e não congelado com efeito imediato (limitado por `Limits.Requests`), envia e-mail a todos os Admins (`AdminRequestRename` com nome antigo e novo) e audita `AuditCollectiveRenamed` com detalhe `"<antigo> → <novo>"`. Nome exatamente igual ao atual é *no-op* (não grava, não audita e não envia e-mail); alteração apenas de maiúsculas/espaçamento é tratada como renomeação real.

### 5. Bloqueios Administrativos de Coletivos e Contas (`/admin/coletivos`, `/admin/contas`)
- **Bloqueios independentes de coletivo (`SetCollectiveBlocks`, `POST /admin/coletivo/bloqueios`):** aplica-se apenas a coletivos aprovados, sem envio de e-mail, gravando `AuditCollectiveBlocks` quando houver mudança efetiva:
  - `EventsBlocked`: impede Administradores do coletivo de criar/editar eventos (o Admin do site não é bloqueado; detalhes completos em `BDR-005`).
  - `Locked`: congela membros e nome — recusa `OwnerApproveJoin`, `RejectJoin` por Administrador, `Promote`, `Demote`, `RenameCollective`, `RequestJoin` e entrada no cadastro com `ErrLocked` (`409`), além de excluir o coletivo de `Collectives.SearchApproved` (autocomplete). **Não** impede decisões do Admin em `AdminApproveJoin` ou `RejectJoin`.
- **Bloqueio e desbloqueio de conta (`BlockAccount`, `UnblockAccount`):**
  - `BlockAccount` (`POST /admin/conta/bloquear`): muda conta `active` → `disabled`, encerra imediatamente todas as sessões (`Sessions.DeleteForUser(ctx, person.ID, nil)`) e audita `AuditAccountBlocked`. Impede autobloqueio (`ErrSelf`, `409`). Não envia e-mail e preserva coletivos/pedidos.
  - `UnblockAccount` (`POST /admin/conta/desbloquear`): muda conta `disabled` → `active` e audita `AuditAccountUnblocked`, sem e-mail.

### 6. Ocultação 404 de `/coletivo/:id` e Serialização Transacional
- **Ocultação 404:** `GET /coletivo/:id` e todas as rotas `POST /coletivo/:id/*` respondem com `answerNotFound` (`404`, idêntico a rota inexistente) para qualquer pessoa que não seja Administrador aprovado de um coletivo aprovado — incluindo estranhos, Membros, Administradores pendentes e **o próprio Admin do site** caso não seja Administrador daquele coletivo. `collectiveAct` verifica `Memberships.Get` antes mesmo de fazer parse do corpo do formulário.
- **Serialização por coletivo (`WithTx` + `Collectives.Update`):** toda operação de escrita que depende do estado ou composição de um coletivo (`AdminApproveJoin`, `OwnerApproveJoin`, `RejectJoin`, `Promote`, `Demote`, `RequestJoin`, `RenameCollective`, `SetCollectiveBlocks` e entrada no cadastro) lê o coletivo dentro de `Store.WithTx` e o grava de volta com `Collectives.Update` (`UpdatedAt = now`). Isso garante conflito e repetição transacional entre operações concorrentes no mesmo coletivo (por exemplo, dois Administradores se rebaixando ao mesmo tempo ou pedido de entrada concorrendo com congelamento).

## Exceções e Casos de Borda

- **Coletivo cujo criador foi excluído:** `Users.Delete` limpa `Collective.CreatedBy`; `CollectivePage.IsMaster` retorna `false`, `ApproveCollective` recusa (`ErrNotAllowed`) e `RejectCollective` conclui sem enviar e-mail (auditoria registra coletivo sem nome de usuário).
- **Pedido ou membro já removido em corrida:** em `POST /coletivo/:id/*`, após a checagem de vínculo do ator passar, um `domain.ErrNotFound` vindo do serviço indica que o pedido ou membro foi decidido por outro Administrador; a página é re-renderizada com `409` (`approvalsMsgDecided`) em vez de `404`.
- **IDs em formulários de `/admin`:** todas as ações `POST /admin/*` enviam `user_id` e `collective_id` em campos ocultos do formulário, nunca no caminho da URL.
- **Limites da fila do Admin (`AdminQueue`):** lê até `domain.ListLimitMax` (`500`) contas pendentes e `500` pedidos aguardando Admin, agrupados em um cartão por pessoa e ordenados pelo pedido mais antigo (ou data de criação da conta se não houver pedidos).

## Prova Executável (BDD)

- **Domínio e normalização:**
  - `internal/domain/model_test.go`: `TestCollectiveKindValid`, `TestCollectiveStatusValid`, `TestMembershipStatusValid`, `TestRoleValid`
  - `internal/domain/rules_test.go`: `TestCollectiveValidate`, `TestMembershipValidate`, `TestNameKey`, `TestCleanCollectiveName`, `TestMsgCollectiveKind`
- **Serviços de aprovação e governança (`internal/approvals`):**
  - `internal/approvals/approvals_test.go`: `TestSentinelErrorsAreDistinct`, `TestCollectivePath`, `TestRoleLabel`, `TestCheckReason`, `TestCheckReasonMessages`, `TestRequireActive`, `TestRequireAdmin`, `TestRequireOwner`
  - `internal/approvals/accounts_test.go`: `TestApproveAccount`, `TestApprovePerson`, `TestApprovePersonSkipsWhatIsNotOpen`, `TestApprovePersonActive`, `TestApprovePersonNothingToDecide`, `TestApprovePersonSkipsForeignPendingCollective`, `TestRejectAccount`, `TestRejectAccountSkipsForeignPendingCollective`, `TestApprovePersonConcurrent`, `TestApproveAndRejectAccountConcurrent`, `TestBlockAccount`, `TestBlockAccountAnotherAdmin`, `TestBlockAccountRefused`, `TestUnblockAccount`, `TestAdminQueue`
  - `internal/approvals/collectives_test.go`: `TestApproveCollective`, `TestApproveCollectiveCreatorWithoutOwnerSide`, `TestApproveCollectiveConcurrent`, `TestRejectCollective`, `TestRejectCollectiveCreatorDeleted`, `TestApproveAndRejectCollectiveConcurrent`, `TestSetCollectiveBlocks`, `TestSetCollectiveBlocksNoChange`, `TestSetCollectiveBlocksOnPendingCollective`
  - `internal/approvals/joins_test.go`: `TestAdminApproveJoin`, `TestOwnerApproveJoin`, `TestJoinDecisionsInEitherOrder`, `TestAdminAndOwnerApproveJoinConcurrent`, `TestRejectJoin`, `TestAdminDecisionsIgnoreTheLock`, `TestPromote`, `TestPromoteRefusesInactiveTarget`, `TestDemote`, `TestDemoteLastAdministrador`, `TestDemoteConcurrent`, `TestCollectiveView`, `TestCollectiveViewRefused`, `TestCollectivePageIsMaster`
  - `internal/approvals/requests_test.go`: `TestRequestJoin`, `TestRequestJoinRefused`, `TestRequestJoinConcurrentSamePerson`, `TestRequestCollective`, `TestRequestCollectiveNameTaken`, `TestRequestCollectiveConcurrentSameName`, `TestRenameCollective`, `TestRenameCollectiveSameKey`, `TestRenameCollectiveNoChange`, `TestRenameCollectiveConcurrent`
- **Rotas HTTP (`internal/web`):**
  - `internal/web/approvals_answer_test.go`: `TestApprovalsAnswer`
  - `internal/web/routes_admin_test.go`: `TestAdminNeedsAnAdmin`, `TestAdminQueueShowsEveryPersonOnce`, `TestAdminActionsFollowTheAccountStatus`, `TestAdminDecisions`, `TestAdminASecondDecisionAnswers409WithAFreshQueue`, `TestAdminAReasonWithAControlCharacterIs422`
  - `internal/web/routes_admin_manage_test.go`: `TestMngPagesNeedAnAdmin`, `TestMngAccountsListsActiveAndBlocked`, `TestMngBlockAccount`, `TestMngUnblockAccount`, `TestMngCollectivesListsTheApprovedOnes`, `TestMngSetCollectiveBlocks`, `TestMngHistoryShowsTheDecisionsNewestFirst`
  - `internal/web/routes_collectives_test.go`: `TestColetivosListsTheMemberships`, `TestColetivosJoin`, `TestColetivosJoinByTypedName`, `TestColetivosJoinLockedCollective`, `TestColetivosNew`, `TestColetivosRateLimit`, `TestCollectiveLinksUseTheSharedBuilder`
  - `internal/web/routes_collective_test.go`: `TestColetivoPageIsInvisibleToEveryoneButItsAdministradores`, `TestColetivoPageShowsTheMembersTheRequestsAndTheForms`, `TestColetivoPageLockedAndEventsBlocked`, `TestColetivoActions`, `TestColetivoSelfDemotion`, `TestColetivoStaleItems`, `TestColetivoRenameRateLimit`
- **Cobertura BDD / UAT planejada:** `@BDD-04-01` a `@BDD-04-08`, `UAT-S3-01` a `UAT-S3-08`, `UAT-S4-01` a `UAT-S4-05`.
