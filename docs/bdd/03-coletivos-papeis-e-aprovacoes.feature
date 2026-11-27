# language: pt
@BDR-004
Funcionalidade: Coletivos, Papéis, Governança e Fluxos de Aprovação

  @BDD-03-01 @BDR-004 @UAT-S6-03 @UAT-S6-06 @UAT-S7-08 @coberto @esperado @seguranca
  Cenário: Ocultação 404 de /coletivo/:id para Membro e Admin não-dono e renomeação auditada pelo Administrador
    Dado que existe um coletivo aprovado com um Administrador, um Membro e um Admin do site que não é Administrador desse coletivo
    Quando o Membro e o Admin do site acessam "GET /coletivo/:id"
    Então ambos recebem 404 idêntico a uma rota inexistente
    Quando o Administrador aprovado acessa "GET /coletivo/:id" e renomeia o coletivo em "POST /coletivo/:id/nome"
    Então a página responde 200 ao Administrador e a renomeação aplica imediatamente
    E o Admin do site recebe o e-mail de notificação na Outbox e "/admin/historico" exibe o registro "coletivo renomeado"

  @BDD-03-02 @BDR-004 @UAT-S6-01 @UAT-S6-02 @coberto @esperado @sem-js
  Cenário: Solicitação de entrada em coletivo aprovado e pedido de novo coletivo em /coletivos
    Dado que um usuário ativo acessa "GET /coletivos"
    Quando solicita entrada em um coletivo aprovado via "POST /coletivos/entrar" (por ID ou por nome digitado sem JS)
    Então o sistema cria o vínculo pendente, notifica Administradores do coletivo e Admins do site, audita "join_requested" e redireciona para "/coletivos?msg=pedido-enviado"
    Quando solicita um novo coletivo ou produtora via "POST /coletivos/novo"
    Então o coletivo é criado como pendente com o solicitante como Master, notifica Admins, audita "collective_requested" e redireciona para "/coletivos?msg=coletivo-pedido"

  @BDD-03-03 @BDR-004 @UAT-S6-06 @coberto @borda
  Cenário: Normalização NameKey garante unicidade ignorando caixa, acentos e espaçamento
    Dado que já existe o coletivo aprovado "Coletivo Águia"
    Quando um usuário tenta criar ou renomear outro coletivo para "  coletivo   aguia  "
    Então o servidor recusa com erro 422 no campo "name" pela colisão de "NameKey"

  @BDD-03-04 @BDR-004 @UAT-S6-04 @UAT-S7-03 @pendente @esperado @concorrencia
  Cenário: Aprovação dupla de pedido de entrada funciona em qualquer ordem com notificações distintas
    Dado que um usuário ativo possui um pedido pendente para entrar em um coletivo aprovado
    Quando um Administrador do coletivo aprova primeiro via "POST /coletivo/:id/pedido/aprovar"
    Então o vínculo permanece pendente aguardando o Admin e nenhum e-mail é enviado ainda ao solicitante
    Quando o Admin aprova primeiro em outro pedido via "POST /admin/pedido/aprovar"
    Então o solicitante recebe e-mail "decision" indicando que aguarda um Administrador do coletivo
    Quando a segunda aprovação ocorre de qualquer um dos lados
    Então o vínculo passa a "approved" e o solicitante recebe e-mail "decision" de aprovação

  @BDD-03-05 @BDR-004 @UAT-S7-03 @coberto @esperado
  Cenário: Aprovação isolada da conta precede aprovação de itens e torna pedidos visíveis aos Administradores
    Dado que existe uma conta em "pending_approval" com um pedido pendente em coletivo aprovado
    Quando o Admin tenta aprovar o pedido antes da conta
    Então o sistema recusa com erro de permissão
    Quando o Admin aprova apenas a conta em "POST /admin/conta/aprovar"
    Então a conta passa para "active", recebe e-mail com botão "Entrar" e os Administradores do coletivo são notificados do pedido

  @BDD-03-06 @BDR-004 @UAT-S7-02 @coberto @esperado
  Cenário: Aprovar tudo consolida conta e pendências próprias em uma transação e um único e-mail
    Dado que existe uma conta em "pending_approval" com um coletivo pendente criado por ela e um pedido em coletivo aprovado
    Quando o Admin executa "POST /admin/pessoa/aprovar-tudo"
    Então a conta, o coletivo próprio e o lado admin do pedido são aprovados na mesma transação
    E a pessoa recebe exatamente um e-mail consolidado e cada decisão gera sua própria entrada em "/admin/historico"

  @BDD-03-07 @BDR-004 @UAT-S7-04 @pendente @esperado @seguranca
  Cenário: Recusa de conta exclui usuário e coletivos pendentes próprios em cascata sem deixar órfãos
    Dado que existe uma conta em "pending_approval" que criou um coletivo pendente e pediu entrada em outro coletivo aprovado
    Quando o Admin recusa a conta em "POST /admin/conta/recusar" com motivo válido
    Então a conta, suas sessões, tokens, vínculos e seu coletivo pendente são removidos em cascata preservando coletivos de terceiros
    E a pessoa recebe e-mail "decision" com o motivo e botão "Criar conta", enquanto "/admin/historico" guarda o snapshot do nome sem e-mail

  @BDD-03-08 @BDR-004 @UAT-S6-05 @coberto @esperado @seguranca
  Cenário: Promoção de Membro ativo, proteção do Master, guarda do último Administrador e autorrebaixamento
    Dado que um coletivo aprovado possui seu Master e um Membro ativo
    Quando o Master promove o Membro em "POST /coletivo/:id/membro/promover"
    Então o Membro torna-se Administrador e a ação é auditada sem envio de e-mail
    Quando o novo Administrador tenta rebaixar o Master em "POST /coletivo/:id/membro/rebaixar"
    Então o servidor responde 409 informando que o Master não pode ser rebaixado
    Quando o novo Administrador rebaixa a si mesmo enquanto o Master ativo permanece
    Então o servidor redireciona para "/coletivos?msg=voce-rebaixado" e "GET /coletivo/:id" passa a retornar 404 para ele

  @BDD-03-09 @BDR-004 @UAT-S6-05 @pendente @concorrencia @seguranca
  Cenário: Serialização transacional impede que dois Administradores se rebaixem ao mesmo tempo deixando o coletivo sem responsável
    Dado que um coletivo aprovado possui exatamente dois Administradores ativos (sem Master)
    Quando ambos disparam "POST /coletivo/:id/membro/rebaixar" simultaneamente
    Então a serialização por "WithTx" e "Collectives.Update" garante que no máximo um rebaixamento seja concluído
    E a segunda transação falha na repetição garantindo ao menos um Administrador ativo no coletivo

  @BDD-03-10 @BDR-004 @UAT-S6-02 @UAT-S6-07 @UAT-S7-07 @coberto @seguranca
  Cenário: Bloqueios independentes EventsBlocked e Locked restringem gestão e sugestões sem bloquear o Admin
    Dado que o Admin ativa "Locked=true" e "EventsBlocked=true" em "POST /admin/coletivo/bloqueios" com motivo
    Quando o sistema salva os bloqueios
    Então o coletivo deixa de aparecer em "GET /coletivos/sugestoes" e recusa novos pedidos de entrada e ações de gestão do Administrador com 409
    Mas o Admin continua podendo decidir pedidos pendentes desse coletivo em "/admin/pedido/aprovar" e "/admin/pedido/recusar"

  @BDD-03-11 @BDR-004 @UAT-S7-06 @coberto @seguranca
  Cenário: Bloqueio e desbloqueio de conta encerra sessões imediatamente e impede autobloqueio do Admin
    Dado que existe uma conta ativa com sessão aberta e um Admin autenticado em "/admin/contas"
    Quando o Admin tenta bloquear a própria conta em "POST /admin/conta/bloquear"
    Então o servidor responde 409 recusando o autobloqueio
    Quando o Admin bloqueia a outra conta ativa
    Então o status muda para "disabled", todas as sessões dessa conta são encerradas imediatamente, a ação é auditada e nenhum e-mail é enviado
    Quando o Admin desbloqueia a conta em "POST /admin/conta/desbloquear"
    Então a conta retorna a "active" e a ação é registrada em "/admin/historico"

  @BDD-03-12 @BDR-004 @UAT-S7-01 @UAT-S7-05 @coberto @seguranca @borda
  Cenário: Rotas administrativas exigem Admin e recusam motivo com caractere de controle
    Dado que um usuário autenticado não-Admin tenta acessar "GET /admin" ou "POST /admin/conta/aprovar"
    Quando a requisição chega ao servidor
    Então o servidor responde 403 com "acesso negado"
    Quando o Admin envia uma recusa com motivo contendo caractere Unicode "\u200B"
    Então o servidor responde 422 recusando o motivo inválido

  @BDD-03-13 @BDR-004 @UAT-S6-01 @browser @acessibilidade
  Cenário: Seletor de coletivo em modo join na página Meus Coletivos
    Dado que um usuário ativo abre "GET /coletivos" em navegador com JavaScript habilitado
    Quando digita o nome parcial de um coletivo aprovado no campo de busca de "collectivePicker"
    Então o componente busca sugestões em "/coletivos/sugestoes", permite navegação por teclado e preenche "collective_id" sem oferecer criação de coletivo novo no formulário de entrada
