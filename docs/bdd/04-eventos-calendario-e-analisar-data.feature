# language: pt
@BDR-005
Funcionalidade: Eventos, Calendário Mensal, Análise de Data e Meus Eventos
  Como pessoa autenticada da comunidade Agenda Clubber Lite
  Quero consultar todos os eventos no calendário e gerenciar os eventos dos meus coletivos
  Para coordenar datas de festas sem colisões e respeitar os bloqueios administrativos

  @BDD-04-01 @BDR-005 @BDR-006 @UAT-S8-02 @UAT-S8-06 @UAT-S9-05 @coberto @esperado @seguranca
  Cenário: Ciclo de vida do evento com criação, visibilidade global, bloqueio de eventos e edição auditada pelo Admin
    Dado um coletivo aprovado "Coletivo Alfa" com um Administrador ativo e um Admin do site ativo
    Quando o Administrador cria um evento futuro válido via "POST /eventos/novo"
    Então o evento fica visível no calendário mensal em "GET /" e em "GET /eventos/dia"
    Quando o Admin bloqueia os eventos do coletivo ("EventsBlocked=true") via "POST /admin/coletivo/bloqueios"
    E o Administrador tenta editar o evento via "POST /eventos/:id/editar"
    Então o servidor responde 409 Conflict com o motivo "os eventos deste coletivo estão bloqueados pelo admin."
    Quando o Admin edita o mesmo evento via "POST /eventos/:id/editar"
    Então a edição é salva com sucesso e uma entrada "event_edited" é registrada em "/admin/historico"

  @BDD-04-02 @BDR-005 @UAT-S8-02 @coberto @esperado
  Cenário: Evento que cruza a meia-noite permanece no dia de início em APP_TZ
    Dado um Administrador aprovado de um coletivo aprovado sem bloqueio no fuso "America/Fortaleza"
    Quando ele cria um evento com início às "23:30" e duração de "4h00"
    Então o horário é armazenado em UTC e o evento aparece exclusivamente no dia civil de início em "America/Fortaleza"
    E o link de ingressos válido ("https://...") é renderizado com "rel=noopener noreferrer nofollow ugc" e "target=_blank"

  @BDD-04-03 @BDR-005 @UAT-S8-03 @coberto @borda
  Esquema do Cenário: Validação de campos críticos na criação de evento
    Dado um Administrador aprovado acessando o formulário de criação de evento
    Quando ele envia "POST /eventos/novo" com o campo "<campo>" preenchido com "<valor>"
    Então o servidor responde 422 Unprocessable Entity
    E exibe a mensagem de erro esperada "<erro>"

    Exemplos:
      | campo      | valor                | erro                                                       |
      | date       | 01/01/2020           | a data não pode ser anterior a hoje                        |
      | ticket_url | javascript:alert(1)  | informe um endereço que comece com http:// ou https://     |
      | ticket_url | ftp://ingressos.test | informe um endereço que comece com http:// ou https://     |
      | price      | 100000,00            | informe um preço de R$ 0,01 a R$ 99999,99                  |
      | duration   | 0h10                 | informe uma duração de 15 minutos a 23h59                  |

  @BDD-04-04 @BDR-005 @UAT-S8-01 @coberto @borda
  Cenário: Evento gratuito ignora preço digitado e valida campos Outros e catálogos IBGE e CircuitoNE
    Dado um Administrador aprovado preenchendo "POST /eventos/novo"
    Quando ele marca "free=on" mesmo enviando texto em "price", escolhe "type=outros" com "type_other" preenchido e "style=outros" com "style_other" preenchido
    E informa uma UF válida do IBGE e uma cidade pertencente a essa UF
    Então o evento é criado com "PriceCents=0" e "Substyle" vazio
    Mas se a cidade não pertencer à UF selecionada ou "type_other"/"style_other" estiver vazio, o servidor responde 422

  @BDD-04-05 @BDR-005 @UAT-S8-04 @coberto @esperado
  Cenário: Edição de evento passado preserva a data original mas impede alterar para outra data passada
    Dado um evento existente cuja data de início em "America/Fortaleza" está no passado
    Quando o Administrador do coletivo edita a descrição mantendo a mesma data civil do evento
    Então o servidor salva as alterações e redireciona para "/eventos/:id?msg=evento-salvo"
    E o coletivo original do evento permanece inalterado
    Mas se ele tentar alterar a data para outro dia no passado, o servidor responde 422 com "a data não pode ser anterior a hoje"

  @BDD-04-06 @BDR-005 @UAT-S8-05 @coberto @concorrencia
  Cenário: Concorrência otimista detecta edição simultânea por versão obsoleta
    Dado um evento existente na versão 1 aberto simultaneamente por dois Administradores
    Quando o primeiro salva uma alteração e a versão passa para 2
    E o segundo tenta salvar enviando "version=1"
    Então o servidor responde 409 Conflict com o alerta "este evento foi alterado por outra pessoa; confira e salve de novo."
    E re-renderiza o formulário com a versão atualizada "2" no campo oculto e um link para o evento atual

  @BDD-04-07 @BDR-005 @UAT-S8-06 @coberto @seguranca
  Cenário: Regras de resposta 403 versus 409 para Membro, estranho e coletivo bloqueado
    Dado um Membro aprovado sem coletivos administrados e um Administrador cujo único coletivo está com "EventsBlocked=true"
    Quando o Membro tenta acessar "GET /eventos/novo", "GET /eventos/:id/editar" ou "POST /eventos/:id/excluir"
    Então o servidor responde 403 Forbidden em todas as tentativas sem revelar motivo de bloqueio
    Quando o Administrador com único coletivo bloqueado tenta acessar "GET /eventos/novo"
    Então o servidor responde 403 Forbidden porque "FormCollectives" não oferece coletivos bloqueados
    E quando esse Administrador acessa "GET /eventos/:id/editar" ou "POST /eventos/:id/excluir" do evento do seu coletivo bloqueado
    Então o servidor responde 409 Conflict com "os eventos deste coletivo estão bloqueados pelo admin."

  @BDD-04-08 @BDR-005 @UAT-S9-01 @UAT-S9-02 @UAT-S9-04 @coberto @esperado
  Cenário: Grade mensal exibe até 3 chips por dia, preserva filtro de localidade ativa e respeita teto de 500 eventos
    Dado eventos distribuídos em múltiplas cidades e um dia com mais de 3 eventos cadastrados
    Quando uma pessoa autenticada acessa "GET /?mes=2026-11&uf=CE&cidade=Fortaleza"
    Então cada célula da grade exibe no máximo 3 chips ordenados por início e o contador "+N" para o restante
    E os links de mês anterior, próximo, hoje e de cada dia preservam "uf=CE" e "cidade=Fortaleza"
    E um parâmetro "?uf=" ou "?cidade=" inválido do catálogo é descartado com segurança sem erro 500
    E quando a consulta atinge o limite de 500 eventos a página sinaliza truncamento ("Truncated")

  @BDD-04-09 @BDR-005 @UAT-S9-05 @coberto @esperado
  Cenário: Analisar Data destaca eventos da mesma cidade no topo e recusa data inválida com 422
    Dado uma data com eventos em "Fortaleza" (CE) e "Recife" (PE)
    Quando uma pessoa autenticada consulta "GET /eventos/dia?data=2026-11-15&destaque_uf=CE&destaque_cidade=Fortaleza"
    Então os eventos de "Fortaleza" aparecem primeiro marcados com "SameCity=true" seguidos pelos demais, ambos ordenados por horário
    Quando ela consulta "GET /eventos/dia?data=invalida"
    Então o servidor responde 422 Unprocessable Entity com "escolha uma data válida (dd/mm/aaaa)" sem ecoar a entrada

  @BDD-04-10 @BDR-005 @UAT-S8-07 @coberto @esperado
  Cenário: Meus Eventos separa Próximos e Anteriores e explica motivos de somente leitura
    Dado um usuário com vínculos aprovados como Administrador em coletivo ativo, Membro em outro e Administrador em coletivo bloqueado
    Quando ele acessa "GET /meus-eventos"
    Então eventos cujo término é posterior ao instante atual aparecem em "Próximos" em ordem crescente de início
    E eventos já encerrados aparecem em "Anteriores" do mais recente ao mais antigo
    E as linhas não editáveis exibem "só leitura (Membro)" para Membro e "eventos bloqueados pelo admin" para Administrador do coletivo bloqueado

  @BDD-04-11 @BDR-005 @UAT-S8-06 @pendente @seguranca
  Cenário: Acesso a evento inexistente ou com ID malformado responde 404 uniforme
    Dado uma pessoa autenticada no sistema
    Quando ela solicita "GET /eventos/id-inexistente", "GET /eventos/id-inexistente/editar" ou "POST /eventos/id-inexistente/excluir"
    Então todas as respostas retornam 404 Not Found idêntico a uma rota não registrada ("answerNotFound")

  @BDD-04-12 @BDR-005 @UAT-S8-06 @pendente @concorrencia
  Cenário: Serialização por coletivo impede criação de evento em corrida com bloqueio administrativo
    Dado um Administrador submetendo "POST /eventos/novo" ao mesmo tempo em que o Admin ativa "EventsBlocked=true" no coletivo
    Quando as duas transações concorrem sobre o mesmo coletivo via "Store.WithTx" e "Collectives.Update"
    Então uma das transações é serializada e reexecutada sobre o estado mais recente
    E nenhum evento é criado indevidamente após a efetivação do bloqueio
