# language: pt
@BDR-001 @BDR-002
Funcionalidade: Ciclo de Vida de Contas e Política de Autenticação

  @BDD-01-01 @BDR-001 @BDR-002 @UAT-S1-04 @UAT-S2-01 @UAT-S2-02 @UAT-S3-01 @UAT-S7-02 @coberto @esperado @seguranca
  Cenário: Cadastro completo com novo coletivo, confirmação sem nome, aprovação pelo Admin e login
    Dado que um visitante preenche o cadastro em "/cadastro" com um nome de coletivo inédito como Administrador
    Quando o visitante envia "POST /cadastro"
    Então o sistema redireciona para "/cadastro/enviado" e enfileira o e-mail "verify_email" na Outbox com saudação fixa "Olá!" sem o nome submetido
    E ao abrir "GET /confirmar?token=<token>" o token não é consumido
    Quando o visitante confirma o e-mail em "POST /confirmar"
    Então a conta passa para "pending_approval" e o Admin recebe notificação "admin_new_signup"
    Quando o Admin aprova tudo em "POST /admin/pessoa/aprovar-tudo"
    Então a conta e o coletivo ficam aprovados e o usuário consegue autenticar em "POST /entrar" e acessar "GET /" com status 200

  @BDD-01-02 @BDR-002 @UAT-S1-02 @UAT-S1-03 @coberto @esperado
  Cenário: Cadastro entrando em coletivo aprovado existente
    Dado que existe um coletivo aprovado e não congelado "Coletivo Alfa" do tipo "coletivo"
    Quando um visitante envia "POST /cadastro" escolhendo "Coletivo Alfa" como "member"
    Então o sistema cria a conta em "pending_email" com vínculo pendente para "Coletivo Alfa"
    E redireciona para "/cadastro/enviado" enfileirando o e-mail "verify_email" válido por 24 horas

  @BDD-01-03 @BDR-002 @UAT-S1-05 @coberto @borda @sem-js
  Cenário: Recusa do papel Membro ao criar um coletivo novo no cadastro sem JavaScript
    Dado que um visitante envia "POST /cadastro" sem selecionar "collective_id" e informando um nome de coletivo inédito
    Quando o papel enviado é "member"
    Então o servidor responde 422 com erro no campo "role" informando que quem cria um coletivo novo entra como administrador (master)
    E os campos de senha não são reimpressos no HTML retornado

  @BDD-01-04 @BDR-001 @BDR-002 @UAT-S1-06 @coberto @seguranca
  Cenário: Anti-enumeração no cadastro quando o e-mail já está registrado
    Dado que já existe uma conta cadastrada com o e-mail "dono@agenda.test"
    Quando um visitante envia "POST /cadastro" com "dono@agenda.test" e dados válidos de coletivo aprovado
    Então o servidor redireciona para "/cadastro/enviado" da mesma forma que um cadastro novo
    E enfileira apenas o e-mail "existing_account" com o nome salvo da conta para "dono@agenda.test" sem criar novo usuário nem token

  @BDD-01-05 @BDR-001 @BDR-002 @UAT-S1-08 @coberto @seguranca
  Cenário: Pré-validação ValidateSignup não consome orçamento por e-mail e respeita limite de 5 por hora por IP
    Dado que o cadastro possui limitadores isolados "Limits.Signup" (5/h por IP) e "Limits.SignupEmail" (5/h por hash de e-mail)
    Quando são enviadas 4 submissões inválidas em "POST /cadastro" para o mesmo e-mail
    Então todas retornam 422 sem consumir o orçamento de "Limits.SignupEmail"
    Quando uma 5ª submissão válida chega dentro do limite de IP
    Então o cadastro é aceito com redirecionamento para "/cadastro/enviado"
    Quando uma 6ª submissão na mesma hora parte do mesmo IP
    Então o servidor responde 429 com mensagem de excesso de tentativas

  @BDD-01-06 @BDR-002 @UAT-S1-07 @coberto @seguranca @borda
  Cenário: Rejeição de caracteres Unicode de formato Cf e separadores de linha no cadastro
    Dado que um visitante prepara um cadastro em "/cadastro"
    Quando envia o nome contendo caractere invisível "\u200B" ou "\u202E" ou o nome do coletivo contendo "\u2028"
    Então o servidor responde 422 com erro de caracteres de controle no campo correspondente
    E nenhum registro de usuário ou coletivo é persistido

  @BDD-01-07 @BDR-002 @UAT-S2-01 @UAT-S2-02 @UAT-S2-03 @coberto @seguranca
  Cenário: Confirmação de e-mail em duas etapas protege contra scanners e rejeita reutilização do link
    Dado que existe uma conta em "pending_email" com token de confirmação válido
    Quando um scanner acessa "GET /confirmar?token=<token>" duas vezes seguidas
    Então ambas as respostas retornam 200 exibindo apenas o botão de confirmação e mantendo o token intacto
    Quando o titular confirma via "POST /confirmar"
    Então a conta muda para "pending_approval" e redireciona para "/aguardando"
    Quando o mesmo token é reenviado em "POST /confirmar"
    Então o servidor responde 400 exibindo o formulário de reenvio com mensagem de link expirado ou já usado

  @BDD-01-08 @BDR-001 @BDR-002 @UAT-S2-04 @UAT-S2-05 @coberto @seguranca
  Cenário: Reenvio de confirmação aposenta link anterior, omite nome e evita enumeração
    Dado que existe uma conta em "pending_email", uma conta "active" e um e-mail não cadastrado
    Quando "POST /confirmar/reenviar" é submetido para cada um dos três endereços
    Então os três retornam 200 com a mesma mensagem neutra de confirmação de reenvio
    E somente a conta em "pending_email" recebe novo e-mail "verify_email" com saudação "Olá!" sem nome, invalidando o token anterior

  @BDD-01-09 @BDR-001 @BDR-002 @UAT-S1-08 @pendente @borda @seguranca
  Cenário: Purga diária remove contas não confirmadas após 7 dias e seus coletivos pendentes
    Dado que existe uma conta em "pending_email" criada há mais de 7 dias com um coletivo pendente criado por ela
    E existe outra conta em "pending_email" criada há 3 dias
    Quando o job diário executa "PurgeUnconfirmedSignups"
    Então a conta antiga e seu coletivo pendente são removidos sem enviar e-mail nem gravar auditoria
    E a conta recente permanece intacta no banco

  @BDD-01-10 @BDR-002 @UAT-S3-02 @coberto @seguranca
  Esquema do Cenário: Login revela estado da conta apenas mediante senha correta
    Dado que existe o cenário de credenciais "<cenario>"
    Quando o usuário envia "POST /entrar" com "<email>" e "<senha>"
    Então o servidor responde 401 sem criar sessão
    E a página exibe o retorno esperado "<mensagem_esperada>"

    Exemplos:
      | cenario            | email                        | senha          | mensagem_esperada                              |
      | pending_email      | pendente-email@agenda.test   | senha-correta  | confirme seu e-mail antes de entrar.           |
      | pending_approval   | pendente-aprovacao@agenda.test | senha-correta | seu cadastro aguarda a aprovação do admin.    |
      | disabled           | bloqueada@agenda.test        | senha-correta  | esta conta está desativada. fale com           |
      | senha_errada       | dono@agenda.test             | senha-errada   | e-mail ou senha incorretos.                    |
      | email_desconhecido | naoexiste@agenda.test        | qualquer-senha | e-mail ou senha incorretos.                    |

  @BDD-01-11 @BDR-002 @UAT-S3-03 @coberto @seguranca
  Esquema do Cenário: Proteção safeNext contra redirecionamento aberto após login
    Dado que um usuário ativo acessa "/entrar" com parâmetro "next" igual a "<next_informado>"
    Quando o usuário autentica com credenciais válidas em "POST /entrar"
    Então o redirecionamento pós-login aponta exatamente para "<destino>"

    Exemplos:
      | next_informado      | destino    |
      | /coletivos          | /coletivos |
      | https://evil.com    | /          |
      | //evil.com          | /          |
      | /\evil.com          | /          |

  @BDD-01-12 @BDR-002 @UAT-S3-01 @UAT-S3-04 @UAT-S3-05 @coberto @seguranca
  Cenário: Rotação de sessão no login, cookie __Host-acl_session, logout e resposta 401 para HTMX
    Dado que um usuário autentica em "POST /entrar"
    Quando a sessão é criada
    Então o cookie "__Host-acl_session" é definido com "HttpOnly", "Secure", "SameSite=Lax" e "Path=/"
    E uma nova autenticação no mesmo navegador revoga o token de sessão anterior
    Quando o usuário encerra a sessão em "POST /sair"
    Então o cookie é expirado e uma requisição HTMX subsequente a "GET /calendario" recebe 401 com cabeçalho "HX-Redirect: /entrar"

  @BDD-01-13 @BDR-002 @UAT-S3-01 @pendente @borda
  Cenário: Sessão desliza por 7 dias de inatividade com atualização a cada 5 minutos e teto absoluto de 30 dias
    Dado que um usuário possui uma sessão ativa criada no instante T0
    Quando o usuário faz requisições antes de 5 minutos desde o último toque
    Então "LastSeenAt" e "ExpiresAt" não são regravados no banco
    Quando o usuário retorna após 6 dias de inatividade
    Então a sessão continua válida e sua expiração desliza sem nunca ultrapassar T0 mais 30 dias
    Quando o usuário fica inativo por 7 dias completos ou atinge 30 dias desde T0
    Então a sessão expira, é removida do banco e o cookie é limpo

  @BDD-01-14 @BDR-001 @BDR-002 @UAT-S4-05 @UAT-S4-06 @coberto @seguranca
  Cenário: Bloqueio compartilhado de 5 tentativas por 15 minutos entre login, troca e verificação de senha
    Dado que uma conta ativa está autenticada sem falhas acumuladas
    Quando ocorrem 5 falhas combinadas de senha atual incorreta entre "POST /senha/trocar", "POST /senha/verificar" e "POST /entrar"
    Então a conta é bloqueada por 15 minutos em "LockedUntil"
    E tentativas durante o bloqueio retornam 429 em "/senha/trocar", "pending" sem Argon2 em "/senha/verificar" e 401 com mensagem genérica em "/entrar"
    Mas erros de campo ou senha atual vazia nunca incrementam o contador de falhas

  @BDD-01-15 @BDR-002 @UAT-S4-03 @UAT-S4-04 @coberto @esperado @seguranca
  Cenário: Senha expirada após 90 dias restringe acesso até troca respeitando histórico das 2 últimas senhas
    Dado que uma conta ativa possui senha alterada há mais de 90 dias e duas sessões abertas
    Quando o usuário faz login e tenta acessar "GET /" ou "GET /perfil"
    Então o middleware "requireFreshPassword" redireciona para "/senha/trocar" liberando apenas "/senha/trocar", "/senha/verificar" e "/sair"
    Quando o usuário tenta trocar para uma das 2 últimas senhas
    Então "POST /senha/trocar" responde 422 recusando reutilização do histórico
    Quando o usuário define uma nova senha inédita válida
    Então a senha é atualizada, a sessão atual permanece ativa e todas as outras sessões são encerradas

  @BDD-01-16 @BDR-002 @UAT-S4-07 @pendente @esperado
  Cenário: Aviso no banner e lembrete único por e-mail nos últimos 7 dias antes da expiração da senha
    Dado que uma conta ativa possui senha com 87 dias de idade (faltam 3 dias para vencer)
    Quando o usuário acessa "GET /" e o job diário executa "SendPasswordReminders" duas vezes no mesmo ciclo
    Então o shell exibe o banner ".expiry-banner" apontando para "/senha/trocar" sem repeti-lo na própria página "/senha/trocar"
    E exatamente um e-mail "password_expiring" é enfileirado para o ciclo atual de senha

  @BDD-01-17 @BDR-002 @UAT-S4-01 @UAT-S4-02 @coberto @esperado @seguranca
  Cenário: Recuperação de senha com link de 1 hora preserva token em erro de campo e encerra todas as sessões
    Dado que uma conta ativa possui sessões abertas e solicita recuperação em "POST /senha/esqueci"
    Quando um link de recuperação "password_reset" válido por 1 hora é enfileirado
    E o usuário abre "GET /senha/redefinir?token=<token>"
    Então o token não é consumido no GET
    Quando o usuário envia "POST /senha/redefinir" com senhas divergentes
    Então o servidor responde 422 e o link permanece utilizável
    Quando o usuário envia uma nova senha válida fora do histórico
    Então a senha é alterada, todas as sessões da conta são encerradas e o reuso do link retorna 400

  @BDD-01-18 @BDR-002 @UAT-S1-01 @UAT-S1-02 @UAT-S1-04 @browser @acessibilidade
  Cenário: Widgets interativos de telefone, checklist de senha e seletor de coletivo no cadastro
    Dado que um visitante acessa "GET /cadastro" em navegador com JavaScript habilitado
    Quando cola "+55 85 98888-7777" no telefone, digita uma senha forte e busca "Alf" no seletor de coletivo
    Então "phoneMask" formata "(85) 98888-7777", "passwordFields" marca todos os 7 critérios com ".is-ok" e texto ".sr-only"
    E "collectivePicker" exibe sugestões via "/coletivos/sugestoes" permitindo escolher coletivo existente ou travar papel em Administrador para nome inédito
