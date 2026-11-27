# language: pt
@BDR-003
Funcionalidade: Edição de Perfil e Troca de E-mail em Duas Etapas

  @BDD-02-01 @BDR-003 @UAT-S5-01 @coberto @esperado
  Cenário: Atualização de nome, telefone e WhatsApp no perfil sem perder sessão nem gerar auditoria
    Dado que um usuário ativo está autenticado e acessa "GET /perfil"
    Quando envia "POST /perfil" com nome válido, telefone de 11 dígitos e "phone_is_whatsapp=1"
    Então o servidor redireciona para "/perfil?msg=perfil-salvo" exibindo ".status--ok" com "perfil salvo."
    E os novos dados ficam persistidos mantendo a sessão ativa e sem criar entrada em "/admin/historico"

  @BDD-02-02 @BDR-003 @UAT-S5-02 @coberto @borda @seguranca
  Cenário: Rejeição de caractere Unicode Cf, telefone inválido e pedido do próprio e-mail atual
    Dado que um usuário ativo está autenticado em "/perfil"
    Quando envia "POST /perfil" com nome contendo "\u200B" ou telefone com menos de 10 dígitos
    Então o servidor responde 422 preservando os valores digitados e exibindo erros de campo
    Quando envia "POST /perfil/email" informando o próprio e-mail atual da conta
    Então o servidor responde 422 com erro de campo "este já é o seu e-mail"

  @BDD-02-03 @BDR-003 @UAT-S5-03 @coberto @seguranca
  Cenário: Troca para e-mail ocupado por outra conta preserva anti-enumeração
    Dado que o e-mail "dono@agenda.test" já pertence a outra conta
    Quando um usuário autenticado envia "POST /perfil/email" solicitando troca para "dono@agenda.test"
    Então o servidor redireciona para "/perfil?msg=email-pendente" com a mesma mensagem neutra de um endereço livre
    E nenhum token "change_email" é criado, enfileirando apenas "existing_account" para "dono@agenda.test"

  @BDD-02-04 @BDR-003 @UAT-S5-04 @pendente @esperado @seguranca
  Cenário: Jornada completa de troca de e-mail em duas etapas com sobrevivência de sessão
    Dado que um usuário ativo autenticado solicita troca para um novo endereço disponível em "POST /perfil/email"
    Quando o pedido é processado
    Então o e-mail "change_email" com link de 24 horas é enfileirado para o novo endereço e "email_change_notice" para o endereço atual
    E o endereço atual continua ativo enquanto "GET /confirmar-email?token=<token>" exibe apenas o botão sem consumir o token
    Quando o usuário confirma em "POST /confirmar-email"
    Então o endereço da conta é atualizado sem enviar segundo e-mail, a sessão atual permanece autenticada e o redirecionamento leva a "/perfil?msg=email-confirmado"

  @BDD-02-05 @BDR-003 @UAT-S5-04 @coberto @borda
  Cenário: Confirmação pública de troca de e-mail funciona para visitante sem sessão ativa
    Dado que existe um token "change_email" válido para uma conta ativa
    Quando um visitante sem cookie de sessão confirma o token em "POST /confirmar-email"
    Então o e-mail da conta é atualizado no banco e a resposta redireciona para "/perfil?msg=email-confirmado"

  @BDD-02-06 @BDR-003 @UAT-S5-05 @UAT-S5-06 @pendente @borda @concorrencia
  Cenário: Nova solicitação aposenta token anterior e endereço tomado no intervalo retorna erro dedicado
    Dado que um usuário solicita duas trocas de e-mail seguidas para endereços livres distintos
    Quando tenta confirmar o primeiro token em "POST /confirmar-email"
    Então o servidor redireciona para "/perfil?msg=email-invalido" pois o token anterior foi aposentado
    Quando outra conta assume o segundo endereço antes da confirmação do segundo token
    Então "POST /confirmar-email" faz rollback e redireciona para "/perfil?msg=email-em-uso"

  @BDD-02-07 @BDR-003 @UAT-S5-05 @coberto @borda @seguranca
  Esquema do Cenário: Tabela fixa perfilNotices mapeia códigos conhecidos e ignora desconhecidos
    Dado que um usuário ativo autenticado acessa "/perfil?msg=<codigo>"
    Quando a página é renderizada
    Então o tipo visual exibido é "<tipo>" e o texto corresponde a "<texto>"

    Exemplos:
      | codigo              | tipo          | texto                                                                                  |
      | perfil-salvo        | status--ok    | perfil salvo.                                                                          |
      | email-pendente      | status--ok    | se o e-mail estiver disponível, enviamos um link de confirmação. ele vale por 24 horas. |
      | email-confirmado    | status--ok    | e-mail confirmado. seu endereço foi atualizado.                                        |
      | email-invalido      | status--error | este link expirou ou já foi usado.                                                     |
      | email-em-uso        | status--error | este e-mail já está em uso por outra conta. peça a troca novamente.                    |
      | codigo-desconhecido | nenhum        |                                                                                        |

  @BDD-02-08 @BDR-003 @UAT-S5-06 @coberto @seguranca
  Cenário: Rate limit protege solicitação e confirmação de troca de e-mail preservando token em 429
    Dado que um usuário autenticado excede 5 pedidos por hora em "POST /perfil/email"
    Quando envia a 6ª tentativa na mesma hora
    Então o servidor responde 429 com mensagem de excesso de tentativas
    Quando um IP excede 10 tentativas por minuto em "POST /confirmar-email"
    Então o servidor responde 429 mantendo o token no campo oculto para nova tentativa posterior

  @BDD-02-09 @BDR-003 @UAT-S5-01 @browser @acessibilidade
  Cenário: Máscara interativa de telefone no formulário de edição de perfil
    Dado que um usuário autenticado abre "GET /perfil" no navegador
    Quando digita 10 ou 11 dígitos no campo de telefone ou cola um número com prefixo "+55"
    Então o componente "phoneMask" formata automaticamente o número sem erros de console nem violações de CSP
