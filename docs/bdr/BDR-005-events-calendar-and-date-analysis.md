---
id: BDR-005
title: "Eventos, Calendário Mensal, Análise de Data e Meus Eventos"
status: accepted
date: 2026-11-26
superseded_by: null
issues: [ACL-127]
scope: [internal/events, internal/catalog, internal/domain, internal/web]
---

# BDR-005: Eventos, Calendário Mensal, Análise de Data e Meus Eventos

## Contexto de Negócio
O Agenda Clubber Lite é um calendário compartilhado e fechado para coletivos e produtoras de festas do Nordeste, criado para dar visibilidade mútua de datas e evitar colisões de agenda entre organizadores. Para equilibrar transparência comunitária, autonomia dos coletivos, integridade de dados geográficos/musicais e controle administrativo contra abusos, o produto precisou formalizar regras claras sobre:

1. **Visibilidade global vs. autoria restrita:** todos os membros autenticados devem enxergar a agenda completa (incluindo eventos passados, para histórico e referência), mas somente Administradores aprovados de coletivos aprovados e não bloqueados podem publicar ou alterar eventos.
2. **Semântica de fuso horário, duração, preço e catálogos:** festas frequentemente cruzam a meia-noite; a classificação de dia, o bloqueio de datas passadas na criação e a consistência de UFs/cidades (IBGE) e estilos/subestilos (CircuitoNE) precisam de uma fonte única de verdade no servidor.
3. **Concorrência otimista e bloqueio administrativo:** múltiplos Administradores do mesmo coletivo podem editar o mesmo evento simultaneamente, e o Admin do site precisa poder bloquear os eventos de um coletivo sem perder sua própria capacidade de moderação.
4. **Consulta por mês, análise de data e gestão própria:** organizadores precisam navegar pela grade mensal filtrada por localidade ativa, analisar colisões em uma data específica (`Analisar Data`) com destaque para a mesma cidade e acompanhar os eventos dos seus coletivos em `Meus Eventos`.

## Regra Acordada

### 1. Visibilidade Global
- Toda pessoa autenticada com conta `active` visualiza o calendário mensal (`GET /` e `GET /calendario`), o detalhe de qualquer evento (`GET /eventos/:id`) e a lista de eventos de um dia (`GET /eventos/dia`), **incluindo eventos passados**, independentemente do coletivo ao qual pertença.

### 2. Matriz de Permissões (`mayCreate`, `mayEdit`, `FormCollectives`, `UserCanCreateEvents`)
As permissões são centralizadas em `internal/events/permissions.go` e compartilhadas entre os fluxos de escrita (dentro de `Store.WithTx`) e as views que decidem quais botões/links exibir:

| Ação | Quem pode executar | Regras e espelhos |
|---|---|---|
| **Criar evento** (`mayCreate`) | Conta `active` que seja `owner` (Administrador) `approved` de um coletivo `approved` com `EventsBlocked == false`. | O Admin do site (`User.IsAdmin`) só cria em coletivos onde também é Administrador aprovado; `EventsBlocked` não bloqueia o Admin no `POST`, porém `events.FormCollectives` **nunca oferece coletivos bloqueados** no formulário (nem para o Admin). O link `Criar Evento` no menu espelha essa regra via `Memberships.UserCanCreateEvents`. |
| **Editar / Excluir evento** (`mayEdit`) | Conta `active` que seja Administrador aprovado do coletivo aprovado do evento (com `EventsBlocked == false`), **ou** o Admin do site (`User.IsAdmin`) em qualquer coletivo. | O evento nunca muda de coletivo (`CollectiveID`, `CreatedBy` e `CreatedAt` são preservados na edição). `DetailView.CanDelete` é idêntico a `DetailView.CanEdit`. |
| **Listar em `Meus Eventos`** (`events.Mine`) | Qualquer conta `active` vê os eventos de todos os coletivos onde possui vínculo `approved` (`owner` ou `member`), qualquer que seja o status do coletivo. | A permissão de edição por linha (`MineItem.CanEdit`) segue exatamente `mayEdit`. |

### 3. Regras de Resposta para Coletivo Bloqueado (`EventsBlocked`)
- Se nenhum coletivo elegível restar em `events.FormCollectives` (ex.: todos os coletivos do usuário estão bloqueados ou o usuário é `Membro`/estrangeiro), `GET /eventos/novo` e `POST /eventos/novo` respondem **403 Forbidden** (`approvalsMsgNotAllowed`), independentemente do que for enviado.
- O status **409 Conflict** com o motivo explícito (`events.ErrBlocked` → `eventsMsgBlocked`: `"os eventos deste coletivo estão bloqueados pelo admin."`) é retornado **exclusivamente** a um Administrador aprovado (não-Admin do site) do coletivo bloqueado ao tentar postar nele, abrir `/eventos/:id/editar`, salvar edição ou excluir um evento desse coletivo.
- Qualquer outro ator recusado (Membro, estranho ou postagem nomeando coletivo do qual não é Administrador) recebe **403 Forbidden** genérico, sem revelar o bloqueio.
- O Admin do site nunca é barrado por `EventsBlocked` em `mayCreate` ou `mayEdit`: edita e exclui qualquer evento e, se postar diretamente para um coletivo bloqueado do qual é Administrador, a criação é aceita.

### 4. Fuso Horário (`APP_TZ`), Datas, Duração e Preço
- **Fuso e dia do evento:** `Event.StartsAt` é armazenado em **UTC**, mas o dia civil do evento é sempre a data de início em `APP_TZ` (padrão `config.DefaultTZ` = `America/Fortaleza`, formato `events.DateLayout` = `dd/mm/aaaa`, relógio 24h `HH:MM`). Um evento que começa às 23:00 e termina após a meia-noite permanece no dia em que começou.
- **Regra de data passada:** na **criação**, a data em `APP_TZ` não pode ser anterior a hoje (`msgDatePast`: `"a data não pode ser anterior a hoje"`). Apenas a data civil importa, não a hora do dia. Na **edição**, é permitido manter a mesma data civil que o evento já possuía (`prev.StartsAt.In(loc)`), mesmo estando no passado; mudar para outra data passada continua proibido.
- **Duração:** entre **15 minutos e 23h59** (`domain.EventDurationMin = 15`, `domain.EventDurationMax = 1439`), informada em horas (`0`–`23`) e minutos (`0`–`59`).
- **Preço:** se `Free == true`, `PriceCents` é `0` e qualquer valor digitado no campo preço é ignorado. Se não for gratuito, o preço deve estar entre **R$ 0,01 e R$ 99.999,99** (`1` a `domain.EventPriceMax = 9_999_999` centavos), aceitando formatos como `25`, `25,50` ou `R$ 1.234,56` (sem adivinhar formato americano) e formatado de volta por `events.FormatCents` (`1.234,56`). Erro de faixa usa `domain.MsgPriceRange`.

### 5. Catálogos Embutidos, Campos `Outros` e Link de Ingressos
- **Fonte única (`internal/catalog`):** 27 UFs e municípios IBGE (`municipios.json`) e estilos/subestilos musicais do CircuitoNE (`estilos.json`), embutidos uma única vez no binário Go (nunca duplicados em `web/static`). A validação é exata (maiúsculas/minúsculas e acentos importam).
- **Tipos de evento (`domain.EventType`):** `festa`, `rave`, `festival`, `workshop`, `feira` e `outros`.
- **Campos `Outros`:** quando `Type == "outros"`, `TypeOther` é obrigatório (1 a `domain.EventOtherMax = 40` caracteres, linha única); caso contrário é descartado/proibido. Quando `Style == "outros"` (`domain.StyleOther`), `StyleOther` é obrigatório (1 a 40 caracteres, linha única) e `Substyle` deve estar vazio. Estilos do catálogo aceitam `Substyle` vazio, um de seus próprios subestilos ou `outros`.
- **Link de ingressos (`domain.TicketURLProblem`):** opcional; quando preenchido, deve ter no máximo `domain.EventURLMax = 2048` caracteres, sem caracteres de controle ou espaços, esquema exclusivamente `http` ou `https` e hostname não vazio. A mesma função valida na entrada, no domínio e na renderização (`ticketHref`).
- **Limites de texto:** `Name` (`2`–`120`), `Venue` (`0`–`120`), `Artists` (`0`–`2000`, multilinha com normalização CRLF/CR → LF) e `Description` (`0`–`4000`, multilinha).

### 6. Concorrência Otimista e Serialização por Coletivo
- Toda criação, edição ou exclusão de evento executa em uma única transação `Store.WithTx`, relê o ator, o coletivo e o vínculo dentro da transação (verificando permissões antes de expor erros de formulário) e grava o coletivo de volta via `Collectives.Update` para serializar operações concorrentes sobre o mesmo coletivo.
- **Versionamento na edição:** todo evento nasce com `Version = 1`. O formulário de edição transporta `version` em campo oculto; `Events.Update` só salva e incrementa `Version` se a versão armazenada ainda coincidir, retornando `domain.ErrConflict` caso contrário. O serviço traduz para `events.ErrChanged` e o handler responde **409 Conflict** (`eventsMsgChanged`), re-renderizando o formulário com os valores digitados, a versão atualizada no campo oculto e o link (`AlertHref`) para o evento como está agora.

### 7. Calendário Mensal, `Analisar Data` e `Meus Eventos`
- **Grade mensal (`events.Month` / `calendarMonth`):** cobre semanas completas de domingo a sábado (`calendarRange`), incluindo dias de meses vizinhos (`InMonth: false`), marcação de hoje (`Today`) e dias passados (`Past`). Cada célula exibe até **3 chips** (`gridShown = 3`) em ordem de início e contabiliza o restante em `More` (`+N`). O filtro de localidade (`?uf=`, `?cidade=`) oferece apenas estados/cidades com eventos existentes (`Events.ListPlaces`) mais o filtro ativo, preservando o filtro em todos os links de navegação e de dias. A consulta aplica o teto de **500 eventos** (`domain.ListLimitMax = 500`), sinalizando `Truncated` quando atingido.
- **Analisar Data (`GET /eventos/dia`):** lista os eventos que iniciam em `?data=` (`AAAA-MM-DD` ou `dd/mm/aaaa`) em `APP_TZ`, respeitando o filtro opcional `?uf=`/`?cidade=`. Quando `?destaque_uf=` e `?destaque_cidade=` nomeiam um par válido do catálogo, os eventos daquela cidade são promovidos para o topo com `SameCity: true` (sem ocultar os demais), mantendo ordem por horário de início dentro de cada grupo. Data ausente ou inválida retorna **422 Unprocessable Entity** com mensagem fixa (`dayDateMsg`: `"escolha uma data válida (dd/mm/aaaa)"`), sem ecoar a entrada.
- **Meus Eventos (`GET /meus-eventos`):** divide os eventos dos coletivos aprovados do usuário em **Próximos** (eventos cujo término `StartsAt + DurationMin` é posterior a `now`, incluindo eventos em andamento, ordenados do mais próximo ao mais distante) e **Anteriores** (já encerrados, do mais recente ao mais antigo), com limite `domain.ListLimitMax = 500`. Linhas somente leitura exibem o motivo na ordem de prioridade: `"só leitura (Membro)"` (`myEventsNoteMember`), `"eventos bloqueados pelo admin"` (`myEventsNoteBlocked`) ou `"só leitura"` (`myEventsNoteOther`, ex.: coletivo não aprovado).

## Exceções e Casos de Borda
- **Evento inexistente ou ID malformado:** `GET /eventos/:id`, `GET/POST /eventos/:id/editar` e `POST /eventos/:id/excluir` respondem com `answerNotFound` (404 idêntico a rota inexistente), sem vazar informações.
- **`collective_id` vazio na criação:** único erro de formulário retornado antes de abrir transação, pois não há coletivo contra o qual checar permissões.
- **Cidade sem UF no filtro ou destaque:** `events.Filter` e `placeFilter` descartam qualquer `cidade` sem `uf` válida do catálogo (ou cidade que não pertença àquela `uf`), evitando rejeição pela camada de armazenamento.
- **Parâmetro `?mes=` inválido no calendário:** em vez de erro, `loadCalendar` assume o mês corrente em `APP_TZ`.
- **Coletivo removido antes de edição/exclusão:** `writeRead` trata coletivo ausente como `ErrNotAllowed` na criação (veio do formulário) e `domain.ErrNotFound` na edição/exclusão (o coletivo do evento deixou de existir).
- **Seleção automática de coletivo único:** ao abrir `/eventos/novo` com exatamente 1 coletivo disponível, ele já vem pré-selecionado.

## Prova Executável (BDD)
- **Permissões, escrita, concorrência otimista e bloqueios (`internal/events`):**
  - `internal/events/permissions_test.go`: `TestPermissions`
  - `internal/events/write_test.go`: `TestWritePermissions`, `TestWriteReReadsTheActor`, `TestCreateNeedsAKnownCollective`, `TestWriteUnknownEvent`, `TestWriteEventOfAGoneCollective`, `TestCreateStoresTheForm`, `TestAdminCreatesAsAdministrador`, `TestCreateWithAnInvalidFormStoresNothing`, `TestUpdateWritesTheForm`, `TestUpdateWithAnInvalidFormChangesNothing`, `TestUpdateWithAStaleVersion`, `TestUpdateKeepsAPastDate`, `TestDeleteRemovesTheEvent`, `TestCreateAgainstABlock`, `TestEditAgainstADemotion`
- **Validação de entrada, fuso, duração, preço, catálogos e URL (`internal/events`, `internal/catalog`, `internal/domain`):**
  - `internal/events/input_test.go`: `TestFormEventCreate`, `TestFormEventEditCopiesWhatTheFormNeverChanges`, `TestFormEventVersion`, `TestFormEventDateAndTime`, `TestFormEventDuration`, `TestFormEventPrice`, `TestFormEventFreeIgnoresThePrice`, `TestFormEventTicketURL`, `TestFormEventTicketURLIsTheStoresRule`, `TestFormEventDateRule`, `TestFormEventPlace`, `TestFormEventOutrosAndStaleTexts`, `TestFormEventAcceptsEveryCatalogEntry`
  - `internal/events/formof_test.go`: `TestFormOfFormatsTheFields`, `TestFormOfFreeAndSmallPrices`, `TestFormatCents`, `TestDateLayout`, `TestFormOfRoundTrips`
  - `internal/catalog/catalog_test.go`: `TestEmbeddedFilesMatchPinnedBlobs`, `TestStylesCounts`, `TestValidStyle`, `TestSubstyles`, `TestStates`, `TestCitiesCoverExactlyTheStates`, `TestValidCity`, `TestTypeLabelAndTypes`
  - `internal/domain/rules_test.go`: `TestEventValidate`, `TestTicketURLProblem`, `TestMsgPriceRange`
- **Grade mensal, Analisar Data, Meus Eventos e rotas HTTP (`internal/events`, `internal/web`):**
  - `internal/events/calendar_test.go`: `TestFilter`, `TestParseDay`, `TestParseMonth`, `TestCalendarGrid`, `TestCalendarFlags`, `TestCalendarEvents`, `TestCalendarTruncated`, `TestCalendarFilterKept`
  - `internal/events/read_test.go`: `TestMonthPlacesEventsOnTheirLocalDay`, `TestMonthFilterAndPlaces`, `TestMonthAndDayTruncated`, `TestDay`, `TestMine`, `TestMineTruncated`, `TestDetail`, `TestFormCollectives`
  - `internal/web/events_answer_test.go`: `TestEventsAnswer`
  - `internal/web/routes_calendar_test.go`: `TestCalendarShowsTheMonth`, `TestCalendarListsEachDayInOrderAndCapsTheCell`, `TestCalendarFilter`, `TestCalendarEveryoneSignedInSeesEveryEvent`, `TestCalendarPartial`, `TestCalendarTruncatedNote`
  - `internal/web/routes_day_test.go`: `TestDayListsTheEventsOfTheDay`, `TestDayFilterRestrictsToThePlace`, `TestDayHighlightsTheCityOfTheForm`, `TestDayRefusesAMissingOrInvalidDate`, `TestDayHTMXAnswersOnlyTheList`, `TestDayTruncatedNote`
  - `internal/web/routes_event_form_test.go`: `TestEvfNewPage`, `TestEvfCreate`, `TestEvfEditPage`, `TestEvfUpdate`
  - `internal/web/routes_events_test.go`: `TestEvdDetailPageShowsTheEvent`, `TestEvdDetailOffersWhatTheViewerMayDo`, `TestEvdTicketLink`, `TestEvdDeleteByAnAdministrador`, `TestEvdDeleteRefusedWithoutTheRight`, `TestEvdDeleteWhileTheCollectivesEventsAreBlocked`
  - `internal/web/routes_my_events_test.go`: `TestMineOwnerRows`, `TestMineMemberRowsAreReadOnly`, `TestMineUnapprovedCollectiveRowIsReadOnly`, `TestMineSplitsUpcomingAndPast`, `TestMineTruncatedNote`
  - `internal/store/mongostore/mongostore_test.go`: `TestStore` (executa a suíte de contrato `storetest.RunEvents` e `storetest.RunMemberships`, incluindo `UserCanCreateEvents`)
- **Rastreabilidade planejada (BDD / UAT):** `@BDD-05-01` a `@BDD-05-12`, `UAT-S5-01` a `UAT-S5-08`.
