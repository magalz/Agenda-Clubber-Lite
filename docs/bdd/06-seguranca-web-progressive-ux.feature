# language: pt
@BDR-007
Funcionalidade: Segurança Web, Progressive Enhancement, Interatividade e Acessibilidade
  Como visitante ou membro autenticado do Agenda Clubber Lite
  Quero navegar com segurança rígida no navegador, funcionamento completo sem JavaScript e interface acessível
  Para usar o calendário e os formulários em qualquer dispositivo sem riscos de XSS, CSRF ou travamentos

  @BDD-06-01 @BDR-007 @UAT-S0-02 @UAT-S0-04 @UAT-S0-07 @coberto @seguranca
  Cenário: CSP estrita e cabeçalhos de segurança presentes em toda resposta inclusive 404 e sem redirect de trailing slash
    Dado o servidor HTTP configurado com "RedirectTrailingSlash=false" e middleware "securityHeaders"
    Quando um cliente solicita "GET /healthz", "GET /entrar", "GET /healthz/" e "GET /static/../../go.mod"
    Então todas as respostas carregam a "Content-Security-Policy" estrita sem "unsafe-inline" nem "unsafe-eval"
    E incluem "X-Frame-Options: DENY", "X-Content-Type-Options: nosniff", "Referrer-Policy", "Permissions-Policy" e "Cross-Origin-Opener-Policy"
    E "/healthz/" e caminhos de traversal retornam 404 Not Found sem redirecionamento 301

  @BDD-06-02 @BDR-007 @UAT-S0-05 @UAT-S0-06 @coberto @seguranca
  Cenário: Proteção CSRF por Origin e Sec-Fetch-Site e limite de corpo de 64 KiB
    Dado uma rota de mutação "POST" exposta pelo servidor
    Quando um cliente envia "POST" sem cabeçalho "Origin", com "Origin" divergente de "APP_ORIGIN" ou com "Sec-Fetch-Site: cross-site"
    Então o middleware "sameOrigin" responde 403 Forbidden com "origem não permitida" antes de consultar a sessão
    Quando um cliente envia "POST" com "Origin" válido e corpo superior a 64 KiB
    Então o middleware "limitBody" responde 413 Request Entity Too Large com "corpo grande demais"

  @BDD-06-03 @BDR-007 @UAT-S0-02 @UAT-S9-05 @coberto @esperado
  Cenário: Contrato HTMX usa 401/204 com HX-Redirect, Cache-Control no-store e troca 422 restrita ao painel de data
    Dado requisições HTMX identificadas pelo cabeçalho "HX-Request: true"
    Quando uma requisição autenticada chega sem sessão válida ou com senha expirada
    Então o servidor responde 401 Unauthorized com cabeçalho "HX-Redirect" em vez de 303
    Quando uma mutação HTMX conclui com redirecionamento
    Então o servidor responde 204 No Content com cabeçalho "HX-Redirect"
    E fragmentos HTMX ("/calendario", "/eventos/dia", "/eventos/:id", "/opcoes/*") retornam "Cache-Control: no-store"
    E no cliente "htmx:beforeSwap" libera status 422 exclusivamente para o alvo "#analise-resultado"

  @BDD-06-04 @BDR-007 @UAT-S11-03 @coberto @sem-js
  Cenário: Progressive enhancement garante criação de evento e filtro de calendário sem JavaScript
    Dado um navegador com JavaScript completamente desabilitado
    Quando um Administrador seleciona UF e estilo em "GET /eventos/novo" e aciona "[↻] ATUALIZAR LISTAS" ("acao=atualizar")
    Então o servidor responde 200 OK com cidades e subestilos preenchidos sem validar nem gravar o evento
    Quando ele preenche os campos restantes e envia o formulário
    Então o servidor cria o evento e responde com redirecionamento 303 See Other
    E no calendário o botão "FILTRAR" permanece visível e submete o filtro via formulário GET comum

  @BDD-06-05 @BDR-007 @UAT-S0-07 @coberto @seguranca
  Cenário: Allowlists fixas de avisos (?msg=) nunca ecoam códigos desconhecidos da URL
    Dado as páginas que aceitam "?msg=" ("/", "/eventos/:id", "/admin", "/admin/contas", "/admin/coletivos", "/coletivo/:id", "/coletivos", "/perfil")
    Quando uma pessoa autenticada acessa qualquer uma dessas rotas com "?msg=<script>alert(1)</script>" ou código desconhecido
    Então nenhum texto de aviso é exibido e o valor da query string nunca é ecoado no HTML
    E apenas códigos presentes nas tabelas "*Notices" exibem a respectiva mensagem fixa

  @BDD-06-06 @BDR-007 @UAT-S11-01 @UAT-S11-02 @browser @acessibilidade
  Cenário: Componente shell gerencia drawer móvel abaixo de 1024px com focus trap e inert no conteúdo principal
    Dado uma pessoa autenticada em viewport abaixo de "1024px" ("375x812")
    Quando ela aciona "[≡] menu" para abrir o drawer lateral ("#menu-lateral") via componente "shell"
    Então "<main id='conteudo'>" recebe o atributo "inert" e o foco de "Tab" e "Shift+Tab" fica preso dentro do drawer
    Quando ela pressiona "Escape"
    Então o drawer fecha e devolve o foco ao botão "[≡] menu"
    E se a janela for redimensionada para ">= 1024px" com o drawer aberto, ele fecha sem mover o foco para o toggle oculto

  @BDD-06-07 @BDR-007 @UAT-S1-01 @UAT-S1-02 @browser @esperado
  Cenário: Componentes phoneMask, passwordFields e collectivePicker funcionam em conformidade com Alpine CSP
    Dado um visitante na página de cadastro "GET /cadastro" com Alpine.js CSP ativo
    Quando ele cola "+55 85 98888-7777" no telefone ("phoneMask"), digita a senha e confirmação ("passwordFields") e busca coletivo ("collectivePicker")
    Então "phoneMask" remove "+55" e formata "(85) 98888-7777" preservando o cursor
    E "passwordFields" atualiza os itens "li[data-rule]" com ".is-ok" e texto ".sr-only" sem tocar na regra de histórico do servidor
    E "collectivePicker" consulta "/coletivos/sugestoes" com debounce de 250ms, navega por teclado e força "Administrador" apenas para nome novo

  @BDD-06-08 @BDR-007 @UAT-S8-01 @UAT-S9-05 @browser @esperado @acessibilidade
  Cenário: Widgets do formulário de eventos (datePicker, timeSpinner, durationSpinner, moneyInput e eventDynamic)
    Dado um Administrador autenticado em "GET /eventos/novo" com JavaScript ativo
    Quando ele interage com "datePicker", "timeSpinner", "durationSpinner", "moneyInput" e "eventDynamic"
    Então "datePicker" abre diálogo com grade "role=grid", marca dias passados como "aria-disabled=true" e aplica "outline-offset: -2px" no foco
    E "timeSpinner" e "moneyInput" aplicam máscara preservando o cursor via "caretAfterRewrite", enquanto "#free" limpa e desabilita "#price"
    E "durationSpinner" mantém a duração entre "0h15" e "23h59"
    E "eventDynamic" recarrega "/opcoes/cidades" e "/opcoes/subestilos" via fetch com "redirect: error", alterna campos "Outros" e sinaliza "panelStale" no painel "Analisar Data"

  @BDD-06-09 @BDR-007 @UAT-S9-01 @UAT-S9-03 @UAT-S9-06 @UAT-S11-01 @browser @concorrencia
  Cenário: Componente calendarPage aplica filas newestOnly, latest(), fallback HANG_WAIT de 8s, sheet e modo mobile
    Dado uma pessoa autenticada na página inicial "GET /" com "calendarPage" inicializado (".calendar-page--js")
    Quando ela dispara cliques rápidos sucessivos na barra de meses e depois abre um chip de evento em "<dialog id='evento-sheet'>"
    Então cada tipo de requisição (mês, sheet, dia) usa sua própria fila "newestOnly" e source HTMX, garantindo que a última escolha ("latest()") vença
    E transições de slide são ignoradas enquanto "#evento-sheet" estiver aberto ou sob "prefers-reduced-motion: reduce"
    E se uma requisição travar além de "8000 ms" ("HANG_WAIT"), a página faz fallback automático para o link nativo
    E abaixo de "640px" tocar no número de um dia carrega a lista em "#calendar-day" sem sair da página

  @BDD-06-10 @BDR-007 @UAT-S0-07 @UAT-S9-04 @UAT-S11-04 @UAT-S11-05 @browser @acessibilidade @desempenho
  Cenário: Identidade visual CircuitoNE, roving tabindex, anéis de foco, alvos >= 24px e reduced motion
    Dado qualquer página autenticada ou pública renderizada no navegador
    Quando o usuário navega por teclado no calendário e nos formulários com "prefers-reduced-motion: reduce"
    Então a interface aplica os tokens dark-only de "tokens.css", fontes locais sem CDN, cantos retos e rodapé IgnisDev
    E o calendário mantém roving tabindex (setas, "Home", "End") sobre ".calendar__daynum"
    E controles focáveis exibem "outline: 2px solid #fff; outline-offset: 2px" (exceto "-2px" em ".date-picker__day:focus-visible"), alvos medem ">= 24px" e animações respeitam "prefers-reduced-motion"
    E o console registra zero erros JS e zero violações CSP

  @BDD-06-11 @BDR-007 @UAT-S0-02 @pendente @seguranca
  Cenário: Extração de IP do cliente respeita TRUSTED_PROXY_CIDRS e ignora CF-Connecting-IP de origem não confiável
    Dado o servidor configurado com "TRUSTED_PROXY_CIDRS" restrito a uma sub-rede confiável
    Quando uma requisição chega de um peer fora da sub-rede enviando "CF-Connecting-IP: 203.0.113.10"
    Então o servidor ignora o cabeçalho falsificado e usa o IP real do peer para logs e rate limiting
    Mas quando a requisição chega de um proxy dentro de "TRUSTED_PROXY_CIDRS", o valor de "CF-Connecting-IP" é aceito
