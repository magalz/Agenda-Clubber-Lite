# language: pt
@BDR-006
Funcionalidade: Trilha de Auditoria e Notificações Transacionais
  Como Admin do site e operador do Agenda Clubber Lite
  Quero rastrear decisões críticas sem vazar e-mails e garantir entrega confiável de notificações via Outbox
  Para manter governança transparente e comunicação transacional resiliente

  @BDD-05-01 @BDR-006 @UAT-S7-08 @UAT-S10-05 @coberto @seguranca
  Cenário: Histórico de auditoria exibe as 200 entradas mais recentes sem endereços de e-mail e preserva snapshots
    Dado múltiplas ações auditadas de conta, coletivo, vínculo e exclusão/edição administrativa de eventos
    E a posterior exclusão de um usuário ou coletivo referenciado nessas entradas
    Quando o Admin acessa "GET /admin/historico"
    Então a página lista até 200 entradas da mais recente para a mais antiga com datas em "dd/mm/aaaa HH:MM" ("America/Fortaleza")
    E nenhuma entrada contém "@" ou qualquer endereço de e-mail
    E os snapshots de "ActorName", "UserName" e "CollectiveName" permanecem legíveis após a exclusão das entidades

  @BDD-05-02 @BDR-006 @BDR-005 @UAT-S8-02 @UAT-S8-04 @UAT-S8-06 @coberto @esperado
  Cenário: Regra de auditoria de eventos distingue criação, edição própria, edição pelo Admin e exclusão
    Dado um coletivo aprovado com um Administrador e um Admin do site que não é Administrador desse coletivo
    Quando o Administrador cria um evento e depois edita esse mesmo evento
    Então nenhuma entrada de auditoria é gravada para criação ou edição própria
    Quando o Admin edita esse evento
    Então uma entrada "event_edited" é gravada com detalhe no formato "<Nome> · <dd/mm/aaaa>" em "APP_TZ"
    Quando o Administrador ou o Admin exclui o evento
    Então uma entrada "event_deleted" é gravada com o nome do evento e a data de início em "APP_TZ"

  @BDD-05-03 @BDR-006 @UAT-S10-04 @coberto @borda
  Cenário: Operações de autoatendimento e expurgo diário não geram entradas de auditoria
    Dado um usuário ativo que atualiza perfil/e-mail e um cadastro não confirmado criado há mais de 7 dias com coletivo pendente
    Quando o usuário altera perfil ou e-mail e o job diário executa "PurgeUnconfirmedSignups"
    Então o cadastro expirado e seu coletivo pendente são removidos sem enviar e-mail
    E nenhuma entrada de auditoria é criada para perfil, troca de e-mail ou expurgo

  @BDD-05-04 @BDR-006 @BDR-001 @UAT-S10-01 @UAT-S10-02 @coberto @seguranca
  Cenário: Renderização dos 10 templates transacionais com escape HTML e saudação sem nome em verify_email
    Dado os 10 templates registrados em "internal/mail/render.go"
    Quando cada template é renderizado com dados válidos incluindo caracteres HTML sensíveis em nomes e motivos
    Então todos geram assunto em linha única, parte "text/plain" e parte "text/html" escapada sem tags brutas
    E "verify_email" utiliza exclusivamente a saudação "Olá!" sem o nome submetido no cadastro ("ACL-48")
    E "existing_account" preserva o nome já armazenado da conta existente

  @BDD-05-05 @BDR-006 @UAT-S7-02 @UAT-S7-03 @UAT-S7-04 @coberto @esperado
  Esquema do Cenário: Seleção do botão CTA e da nota de conta pendente no template decision.tmpl
    Dado uma decisão administrativa ou de coletivo preparada para "TemplateDecision"
    Quando o template é renderizado com "Button" igual a "<botao>"
    Então o botão HTML e o link apontam para o destino correspondente ao botão "<botao>"
    E a nota condicional de conta aguardando aprovação está "<nota_visivel>"

    Exemplos:
      | botao              | nota_visivel |
      | Entrar             | presente     |
      | Ver meus coletivos | ausente      |
      | Criar conta        | ausente      |

  @BDD-05-06 @BDR-006 @UAT-S10-03 @coberto @borda
  Cenário: Outbox Claim recusa lease não positivo e protege mensagens em voo com lease atômico
    Dado mensagens pendentes e vencidas na coleção "outbox"
    Quando o worker chama "Outbox.Claim" com "lease <= 0" ou "limit < 1"
    Então nenhuma mensagem é retornada nem marcada com "ClaimedUntil" ("ACL-37")
    Quando o worker reivindica uma mensagem com lease positivo de 5 minutos
    Então chamadas concorrentes não recebem a mesma mensagem até que o lease expire ou seja concluído

  @BDD-05-07 @BDR-006 @UAT-S10-03 @coberto @esperado
  Cenário: Política de retentativas do worker segue backoff exponencial e desiste após 6 tentativas
    Dado uma mensagem enfileirada na outbox cujo envio SMTP falha continuamente
    Quando o worker processa as tentativas sucessivas respeitando o relógio
    Então os intervalos entre falhas seguem "1m", "5m", "30m", "2h" e "6h"
    E na 6ª falha a mensagem é marcada com "GaveUp=true" e não volta a ser reivindicada
    E "LastError" é limitado a 500 caracteres sem credenciais nem endereço do destinatário

  @BDD-05-08 @BDR-006 @UAT-S10-03 @coberto @seguranca
  Cenário: Falha no comando SMTP QUIT após entrega não gera reenvio duplicado nem mascara erro de envio
    Dado um servidor SMTP que aceita a entrega da mensagem mas falha ao fechar a conexão ("QUIT")
    Quando "SMTPSender.Send" conclui "SendWithSMTPClient" sem erro e "CloseWithSMTPClient" retorna erro
    Então o remetente registra um aviso em log e retorna sucesso ("nil") para evitar entrega duplicada ("ACL-38")
    Mas se "SendWithSMTPClient" falhar, o erro original de envio é retornado sem ser mascarado pelo erro de fechamento

  @BDD-05-09 @BDR-006 @UAT-S10-03 @coberto @esperado
  Cenário: Seleção de remetente em desenvolvimento entre MAIL_DROP_DIR, SMTP_HOST e modo sem envio
    Dado o comando "agenda serve" executado com "APP_ENV=dev"
    Quando "MAIL_DROP_DIR" está configurado junto com "SMTP_HOST"
    Então "MAIL_DROP_DIR" tem precedência e grava arquivos ".eml" atomicamente com "From=SMTP_FROM"
    Quando nem "MAIL_DROP_DIR" nem "SMTP_HOST" estão configurados em "dev"
    Então o servidor registra um único aviso de envio desabilitado, não inicia o worker e mantém as mensagens na outbox

  @BDD-05-10 @BDR-006 @UAT-S10-03 @pendente @seguranca
  Cenário: Redação de credenciais e destinatário em falhas SMTP durante fluxo completo de outbox
    Dado um worker enviando mensagem via "SMTPSender" autenticado cujo servidor recusa o destinatário ecoando e-mail e login
    Quando o worker registra a falha via "Outbox.MarkFailed" e escreve no log estruturado "slog"
    Então nem o log nem o campo "LastError" armazenado na outbox contêm usuário SMTP, senha ou e-mail do destinatário
