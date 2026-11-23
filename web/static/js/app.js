// Alpine CSP build: it has no expression evaluator, so a template attribute may only name a property or
// method registered here with Alpine.data(): x-data="shell", @click="toggle", :class="sidebarClass".
// No negation, ternary, string literal or call arguments in the template; put that logic in a getter or method.
// Every component here only enhances server-rendered markup: without it the pages still work.
(() => {
  'use strict';

  // The sidebar is a column from this width and a drawer below it (components.css).
  const DESKTOP = '(min-width: 1024px)';

  // The tab stops the open drawer keeps the focus inside. Hidden controls are filtered out by the handler.
  const FOCUSABLE =
    'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

  // Phone: DDD + number, 10 or 11 digits (internal/domain PhoneMin, PhoneMax).
  const PHONE_MAX = 11;

  // Password policy: the same rules as auth.CheckPolicy (internal/auth/password.go), Unicode-aware.
  // Length is counted in code points; symbol is anything that is neither a letter nor a decimal digit.
  const PASSWORD_MIN = 8;
  const PASSWORD_MAX = 128;
  const LOWER = /\p{Ll}/u;
  const UPPER = /\p{Lu}/u;
  const DIGIT = /\p{Nd}/u;
  const SYMBOL = /[^\p{L}\p{Nd}]/u;

  // Collective autocomplete: wait this long after the last key, and from this many characters.
  const SUGGEST_DELAY = 250;
  const SUGGEST_MIN = 2;
  const SUGGEST_URL = '/coletivos/sugestoes';

  const HINT_NEW =
    'nome novo: quem cria um coletivo novo entra como administrador (master). para entrar como membro, escolha um já cadastrado na lista';

  // (00) 0000-0000 up to 10 digits, (00) 00000-0000 with 11. No literal is added before the digit that needs it,
  // so deleting a digit never leaves a dangling "-" or ") ".
  function maskPhone(digits) {
    if (digits === '') return '';
    if (digits.length <= 2) return '(' + digits;
    const split = digits.length > 10 ? 7 : 6;
    const head = '(' + digits.slice(0, 2) + ') ' + digits.slice(2, split);
    return digits.length > split ? head + '-' + digits.slice(split) : head;
  }

  // Index in text just after its n-th digit (0 when n is 0).
  function caretAfterDigits(text, n) {
    if (n <= 0) return 0;
    let seen = 0;
    for (let i = 0; i < text.length; i++) {
      if (text[i] >= '0' && text[i] <= '9' && ++seen === n) return i + 1;
    }
    return text.length;
  }

  // The rules that need only the password; not_email is handled apart because it needs the e-mail.
  const PASSWORD_RULES = {
    min_length: (pw) => [...pw].length >= PASSWORD_MIN,
    lower: (pw) => LOWER.test(pw),
    upper: (pw) => UPPER.test(pw),
    digit: (pw) => DIGIT.test(pw),
    symbol: (pw) => SYMBOL.test(pw),
    max_length: (pw) => [...pw].length <= PASSWORD_MAX,
  };

  // auth.differsFromEmail: ignoring case and surrounding space, the password is neither the whole e-mail nor its
  // local part (when that has three or more code points). A blank e-mail has nothing to differ from.
  function differsFromEmail(password, email) {
    const addr = email.trim().toLowerCase();
    if (addr === '') return true;
    const pw = password.trim().toLowerCase();
    if (pw === addr) return false;
    const at = addr.lastIndexOf('@');
    if (at > 0) {
      const local = addr.slice(0, at);
      if ([...local].length >= 3 && pw === local) return false;
    }
    return true;
  }

  // ---- Event form widgets (partials/event-fields): date picker, time and duration spinners, BRL price ----

  // The limits of internal/domain (EventDurationMin, EventDurationMax, EventPriceMax): minutes, minutes and cents.
  const DURATION_MIN = 15;
  const DURATION_MAX = 23 * 60 + 59;
  const PRICE_MAX = 9999999;

  const MSG_CLOCK_FORMAT = 'use o formato HH:MM';
  const MSG_CLOCK_HOURS = 'hora inválida: use de 00 a 23';
  const MSG_CLOCK_MINUTES = 'minutos inválidos: use de 00 a 59';
  const MSG_HOURS = 'horas: use de 0 a 23';
  const MSG_MINUTES = 'minutos: use de 0 a 59';
  const MSG_DURATION_MIN = 'a duração mínima é de 15 minutos';
  const MSG_DURATION_MAX = 'a duração máxima é de 23h59';
  const MSG_PRICE_MAX = 'o preço máximo é R$ 99.999,99';
  // The mask reads the digits as cents, so "25" would be R$ 0,25: the hint of the form says so once the script runs.
  const HINT_PRICE = 'digite só os números: 2550 vira 25,50. fica de fora se o evento for Free';

  // The choice that opens a free-text field, for the type and for the style (domain.EventTypeOutros, domain.StyleOther).
  const OTHER = 'outros';
  // What the dynamic form asks of the server: the option lists of the cascades (routes_options.go) and the day list.
  const CITIES_URL = '/opcoes/cidades';
  const SUBSTYLES_URL = '/opcoes/subestilos';
  const DAY_URL = '/eventos/dia';
  // Where the day list goes (the "Na mesma data" panel of pages/app/evento-form), and how many rows enter one after
  // another (the rest come in with the last of them, so a long day does not take seconds to show).
  const RESULT_ID = 'analise-resultado';
  const STAGGER_MAX = 8;
  const MSG_DAY_FAILED = '[erro] não foi possível carregar os eventos desta data. tente de novo';

  // The calendar page (calendarPage): the month alone is GET /calendario with the query of the page's own link; the
  // grid lists the chips from this width on (components.css), below it the day's number loads the day list instead.
  const MONTH_URL = '/calendario';
  const WIDE = '(min-width: 640px)';
  const REDUCED_MOTION = '(prefers-reduced-motion: reduce)';
  // How long a month's slide keeps its direction on the root (components.css reads it): the transition is 180ms.
  const SLIDE_KEEP = 1000;
  // How long after its answer a swap that runs in a view transition may still come (see swapped).
  const SWAP_WAIT = 2000;
  // How long a request of the calendar may stay out before the page follows the plain link of the newest one (see
  // newestOnly): a request that hangs must not trap the person.
  const HANG_WAIT = 8000;

  // Each element enters after the one before it. .animate-fade-up reads --i, which the CSSOM sets because a style
  // attribute would break the CSP; the places past STAGGER_MAX all come with the last of them. Under
  // prefers-reduced-motion base.css cuts the animation and its delay short, so the callers need no check of their own.
  function stagger(elements) {
    elements.forEach((element, i) => {
      element.classList.add('animate-fade-up');
      element.style.setProperty('--i', String(Math.min(i, STAGGER_MAX)));
    });
  }

  // Runs an htmx request and tells whether its answer was swapped in. htmx resolves the request's promise once it has
  // handled the answer, but the browser runs the swap of a view transition a frame later, so the promise cannot tell:
  // the swap's own event does (htmx:afterSwap, which comes with the new content in the page). An error status, a request
  // that fails and an answer that swaps nothing give false; the last one is told SWAP_WAIT after the answer. Only the
  // events of target count: they bubble to the document from every request, so another request's answer (a day list, an
  // earlier month) must not settle this one.
  function swapped(request, target) {
    return new Promise((resolve) => {
      let settled = false;
      let timer = 0;
      const finish = (done) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        document.removeEventListener('htmx:afterSwap', onSwap);
        document.removeEventListener('htmx:responseError', onError);
        resolve(done);
      };
      const onSwap = (event) => {
        if (event.detail.target === target) finish(true);
      };
      const onError = (event) => {
        if (event.detail.target === target) finish(false);
      };
      document.addEventListener('htmx:afterSwap', onSwap);
      document.addEventListener('htmx:responseError', onError);
      request().then(() => {
        if (!settled) timer = setTimeout(finish, SWAP_WAIT, false);
      }, () => finish(false));
    });
  }

  // Runs the jobs of one kind of request (run(job, newer)) one at a time, and of those that come while one is out only the
  // newest waits: latest wins. newer() tells a running job that another waits behind it, so it can leave the page as it
  // is for the newer one; send.newest() is the newest job asked for, the one that is out or waiting, else null. A job
  // that stays out longer than HANG_WAIT hands the page to follow(job of the newest), the plain link, so a request that
  // never answers does not turn the person's next clicks into a queue that never moves.
  function newestOnly(run, follow) {
    let current = null;
    let waiting = null;
    const newer = () => waiting !== null;
    const send = async (job) => {
      if (current !== null) {
        waiting = job;
        return;
      }
      current = job;
      let hang = 0;
      try {
        while (current !== null) {
          hang = setTimeout(() => follow(waiting ?? current), HANG_WAIT);
          await run(current, newer);
          clearTimeout(hang);
          current = waiting;
          waiting = null;
        }
      } finally {
        clearTimeout(hang);
        current = null;
        waiting = null;
      }
    };
    send.newest = () => waiting ?? current;
    return send;
  }

  const MONTHS = [
    'janeiro', 'fevereiro', 'março', 'abril', 'maio', 'junho',
    'julho', 'agosto', 'setembro', 'outubro', 'novembro', 'dezembro',
  ];
  // [header, full name] of each weekday, Sunday first as in the calendar.
  const WEEKDAYS = [
    ['Dom', 'domingo'], ['Seg', 'segunda-feira'], ['Ter', 'terça-feira'], ['Qua', 'quarta-feira'],
    ['Qui', 'quinta-feira'], ['Sex', 'sexta-feira'], ['Sáb', 'sábado'],
  ];

  // Which way each key steps a spinner. The start time also takes PageUp and PageDown, an hour at a time.
  const ARROW_KEYS = new Map([['ArrowUp', 1], ['ArrowDown', -1]]);
  const CLOCK_KEYS = new Map([...ARROW_KEYS, ['PageUp', 60], ['PageDown', -60]]);
  // How many days each arrow key moves the date picker's cursor.
  const DAY_KEYS = new Map([['ArrowLeft', -1], ['ArrowRight', 1], ['ArrowUp', -7], ['ArrowDown', 7]]);

  const pad2 = (n) => String(n).padStart(2, '0');

  // A day is an AAAA-MM-DD string, so days compare as text. The arithmetic runs in UTC: no time zone or DST shifts it.
  function isoOf(date) {
    return String(date.getUTCFullYear()).padStart(4, '0') + '-' + pad2(date.getUTCMonth() + 1) + '-' + pad2(date.getUTCDate());
  }

  // The Date (UTC midnight) of a day that exists, or null.
  function dayOf(iso) {
    const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso);
    if (!m) return null;
    const date = new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])));
    return isoOf(date) === iso ? date : null;
  }

  const daysIn = (date) => new Date(Date.UTC(date.getUTCFullYear(), date.getUTCMonth() + 1, 0)).getUTCDate();

  function addDays(iso, n) {
    const date = dayOf(iso);
    date.setUTCDate(date.getUTCDate() + n);
    return isoOf(date);
  }

  // The day moves along, stopping at the end of a shorter month (31 Jan + 1 month = 28 Feb).
  function addMonths(iso, n) {
    const date = dayOf(iso);
    const day = date.getUTCDate();
    date.setUTCDate(1);
    date.setUTCMonth(date.getUTCMonth() + n);
    date.setUTCDate(Math.min(day, daysIn(date)));
    return isoOf(date);
  }

  // dd/mm/aaaa (d/m/aaaa too, as the server reads it) to AAAA-MM-DD, or '' when it is not a day that exists.
  function isoFromText(text) {
    const m = /^(\d{1,2})\/(\d{1,2})\/(\d{4})$/.exec(text.trim());
    if (!m) return '';
    const iso = m[3] + '-' + pad2(m[2]) + '-' + pad2(m[1]);
    return dayOf(iso) ? iso : '';
  }

  const textFromIso = (iso) => iso.slice(8) + '/' + iso.slice(5, 7) + '/' + iso.slice(0, 4);

  // A complete H:MM or HH:MM time (the server's parseClock) as minutes since midnight, or null.
  function clockMinutes(text) {
    const m = /^(\d{1,2}):(\d{2})$/.exec(text.trim());
    if (!m || Number(m[1]) > 23 || Number(m[2]) > 59) return null;
    return Number(m[1]) * 60 + Number(m[2]);
  }

  const clockText = (total) => pad2(Math.floor(total / 60)) + ':' + pad2(total % 60);

  // The start time as it is typed: digits and one ":", the colon added after the second digit while adding (not
  // deleting). Returns { text } to show, or { error } when raw cannot become a time between 00:00 and 23:59.
  function typedClock(raw, adding) {
    let [hours, minutes, ...extra] = raw.replace(/[^\d:]/g, '').replace(/:+/g, ':').split(':');
    if (minutes === undefined && hours.length > 2) {
      minutes = hours.slice(2);
      hours = hours.slice(0, 2);
    }
    if (extra.length > 0 || hours.length > 2 || (minutes !== undefined && (hours === '' || minutes.length > 2))) {
      return { error: MSG_CLOCK_FORMAT };
    }
    if (hours !== '' && Number(hours) > 23) return { error: MSG_CLOCK_HOURS };
    // A single minute digit must be able to become a minute below 60 (the server wants two digits).
    if (minutes && Number(minutes) > (minutes.length === 1 ? 5 : 59)) return { error: MSG_CLOCK_MINUTES };
    if (minutes === undefined && hours.length === 2 && adding) minutes = '';
    return { text: minutes === undefined ? hours : hours + ':' + minutes };
  }

  // The day in a date field as AAAA-MM-DD, as the server reads it (parseDate in internal/events: dd/mm/aaaa, d/m/aaaa or
  // AAAA-MM-DD, a day that exists, the past allowed), or '' when it is not one.
  function dayFromField(text) {
    const typed = text.trim();
    return dayOf(typed) ? typed : isoFromText(typed);
  }

  // The request in flight to refill each select, so a newer choice drops the older one.
  const refills = new WeakMap();

  // Refills a select with the <option> lines a route answers for the query pairs (the routes read uf, estilo and
  // selecionada, not the names of the selects; the server keeps the selected one when it is still valid). Resolves true
  // when it refilled, false when the request failed (the select stays as it was) and null when a newer one replaced it.
  async function refill(select, path, pairs) {
    refills.get(select)?.abort();
    const controller = new AbortController();
    refills.set(select, controller);
    try {
      // A redirect is a failure: an expired session answers 303 to /entrar, and its page must not land in the select.
      const response = await fetch(path + '?' + new URLSearchParams(pairs), {
        headers: { Accept: 'text/html' },
        redirect: 'error',
        signal: controller.signal,
      });
      if (!response.ok) throw new Error('options ' + response.status);
      const html = await response.text();
      if (controller.signal.aborted) return null;
      select.innerHTML = html;
      return true;
    } catch {
      return controller.signal.aborted ? null : false;
    }
  }

  const BRL = new Intl.NumberFormat('pt-BR', { minimumFractionDigits: 2, maximumFractionDigits: 2 });

  // Cents as 1.250,00; empty for zero, so the field is empty again.
  const formatCents = (cents) => (cents > 0 ? BRL.format(cents / 100) : '');

  // The price the server reads (parsePrice in internal/events: "25", "25,5", "1.250,00", with or without "R$") as
  // cents, or 0 when it does not parse or is above the limit.
  function centsFromText(text) {
    const m = /^(?:R\$)?\s*(\d{1,3}(?:\.\d{3})+|\d+)(?:,(\d{1,2}))?$/.exec(text.trim());
    if (!m) return 0;
    const cents = Number(m[1].replace(/\./g, '')) * 100 + Number((m[2] || '').padEnd(2, '0'));
    return cents <= PRICE_MAX ? cents : 0;
  }

  document.addEventListener('alpine:init', () => {
    // App shell: the mobile drawer. The toggle button is the topbar's x-ref="toggle".
    Alpine.data('shell', () => {
      let query;
      let onBreakpoint;
      let onKeydown;

      return {
        open: false,
        // The sidebar is a column from DESKTOP, so the drawer and its trap exist only while this is false.
        wide: false,
        get expanded() {
          return this.open ? 'true' : 'false';
        },
        get sidebarClass() {
          return this.open ? 'is-open' : '';
        },
        get toggleLabel() {
          return this.open ? '[✕] fechar' : '[≡] menu';
        },
        // Main is inert only while the drawer covers the page; the wide sidebar is part of the page.
        get mainInert() {
          return this.open && !this.wide;
        },
        toggle() {
          this.open = !this.open;
        },
        close() {
          if (!this.open) return;
          this.open = false;
          this.$refs.toggle.focus();
        },
        // Tab wraps at the drawer's ends. Esc stays in the template's window handler and close() keeps the
        // focus return. Focus arriving from the topbar is brought in, so it cannot wander through the page.
        onDrawerKeydown(event) {
          if (event.key !== 'Tab' || !this.mainInert) return;
          const drawer = this.$root.querySelector('#menu-lateral');
          const items = [...drawer.querySelectorAll(FOCUSABLE)].filter((item) => item.offsetParent !== null);
          if (items.length === 0) return;
          const first = items[0];
          const last = items[items.length - 1];
          const active = document.activeElement;
          if (!drawer.contains(active)) {
            event.preventDefault();
            (event.shiftKey ? last : first).focus();
            return;
          }
          if (event.shiftKey && active === first) {
            event.preventDefault();
            last.focus();
          } else if (!event.shiftKey && active === last) {
            event.preventDefault();
            first.focus();
          }
        },
        // From 1024px the toggle is hidden, so close without moving focus to it.
        init() {
          query = window.matchMedia(DESKTOP);
          this.wide = query.matches;
          onBreakpoint = (event) => {
            this.wide = event.matches;
            if (event.matches) this.open = false;
          };
          query.addEventListener('change', onBreakpoint);
          onKeydown = (event) => this.onDrawerKeydown(event);
          window.addEventListener('keydown', onKeydown);
        },
        destroy() {
          query.removeEventListener('change', onBreakpoint);
          window.removeEventListener('keydown', onKeydown);
        },
      };
    });

    // Phone mask on the input that carries x-data="phoneMask" @input="format". The masked text is what posts; the
    // server keeps only the digits. The caret stays after the digit it was after, so editing in the middle works
    // and a backspace over a literal moves the caret back instead of getting stuck.
    Alpine.data('phoneMask', () => ({
      format(event) {
        const input = event.target;
        const typed = input.value;
        let digits = typed.replace(/\D/g, '');
        let caretDigits = typed.slice(0, input.selectionStart ?? typed.length).replace(/\D/g, '').length;
        // A pasted or autofilled "+55 85 9...": drop the country code. Typing is never touched, because 55 is a DDD.
        if (event.inputType !== 'insertText' && digits.length > PHONE_MAX && digits.startsWith('55')) {
          digits = digits.slice(2);
          caretDigits -= 2;
        }
        digits = digits.slice(0, PHONE_MAX);
        const masked = maskPhone(digits);
        if (masked === typed) return;
        input.value = masked;
        if (document.activeElement === input) {
          const caret = caretAfterDigits(masked, Math.min(caretDigits, digits.length));
          input.setSelectionRange(caret, caret);
        }
      },
    }));

    // The password partial (partials/password-fields.html): ticks the checklist while the person types and says
    // whether the two passwords match. The "history" rule is never touched, only the server can check it.
    Alpine.data('passwordFields', () => {
      let password;
      let confirm;
      let items;
      let emailInput = null;
      let fixedEmail = null;
      let onEmailInput;

      return {
        // '', 'ok' or 'error': the state of the confirmation message.
        match: '',
        get matchText() {
          if (this.match === 'ok') return 'as senhas conferem';
          if (this.match === 'error') return 'as senhas não conferem';
          return '';
        },
        get matchClass() {
          return this.match === '' ? '' : 'match-status--' + this.match;
        },
        init() {
          const root = this.$root;
          password = root.querySelector('#password');
          confirm = root.querySelector('#password_confirm');
          items = root.querySelectorAll('li[data-rule]');
          if (root.dataset.emailInput) emailInput = document.getElementById(root.dataset.emailInput);
          if (root.dataset.email !== undefined) fixedEmail = root.dataset.email;
          if (emailInput) {
            onEmailInput = () => this.update();
            emailInput.addEventListener('input', onEmailInput);
          }
          this.update();
        },
        destroy() {
          if (emailInput) emailInput.removeEventListener('input', onEmailInput);
        },
        // The live e-mail input wins over a fixed address; with neither, not_email is left to the server.
        email() {
          if (emailInput) return emailInput.value;
          return fixedEmail;
        },
        update() {
          const pw = password.value;
          const email = this.email();
          for (const item of items) {
            const rule = item.dataset.rule;
            let ok;
            if (rule === 'not_email') {
              if (email === null) continue;
              ok = pw !== '' && differsFromEmail(pw, email);
            } else if (PASSWORD_RULES[rule]) {
              ok = pw !== '' && PASSWORD_RULES[rule](pw);
            } else {
              continue;
            }
            item.classList.toggle('is-ok', ok);
            const status = item.querySelector('.sr-only');
            if (status) status.textContent = ok ? ' (atendido)' : ' (pendente)';
          }
          if (confirm.value === '') this.match = '';
          else this.match = confirm.value === pw ? 'ok' : 'error';
        },
      };
    });

    // Collective picker on the signup form: #collective_name is a combobox over the suggestions that
    // /coletivos/sugestoes returns into #collective-suggestions-box (partials/signup-suggestions.html).
    // Picking an option fills the hidden #collective_id; any typing clears it. A name with no pick is a NEW
    // collective, whose creator is its owner: the script then selects Administrador and disables Membro.
    // With data-mode="join" on the root (Meus Coletivos, pages/app/coletivos.html) the picker only joins an
    // existing collective: there is no #role-hint, the role is never changed, and a typed name just clears
    // #collective_id, so the form cannot post a name with no pick as a new collective.
    Alpine.data('collectivePicker', () => {
      let nameInput;
      let idInput;
      let box;
      let roleHint;
      let hintDefault;
      let owner;
      let joinOnly = false;
      let timer = 0;
      let request = null;
      let options = [];
      let active = -1;

      return {
        expanded: 'false',
        activeDescendant: null,
        membroDisabled: false,

        init() {
          const root = this.$root;
          nameInput = root.querySelector('#collective_name');
          idInput = root.querySelector('#collective_id');
          box = root.querySelector('#collective-suggestions-box');
          joinOnly = root.dataset.mode === 'join';
          if (!joinOnly) {
            roleHint = root.querySelector('#role-hint');
            hintDefault = roleHint.textContent;
            owner = root.querySelector('input[name="role"][value="owner"]');
          }
          this.syncRole();
        },
        destroy() {
          this.cancel();
        },

        // The kind radio picked in the form, or '' when none is.
        kind() {
          const checked = this.$root.querySelector('input[name="collective_kind"]:checked');
          return checked ? checked.value : '';
        },
        // A name typed and no collective picked: the form would create a new one.
        syncRole() {
          if (joinOnly) return;
          const isNew = nameInput.value.trim() !== '' && idInput.value === '';
          this.membroDisabled = isNew;
          if (isNew) owner.checked = true;
          roleHint.textContent = isNew ? HINT_NEW : hintDefault;
        },

        // Stops what is waiting or in flight, so an old answer never lands on newer text.
        cancel() {
          clearTimeout(timer);
          if (request) request.abort();
          request = null;
        },
        close() {
          this.cancel();
          box.replaceChildren();
          options = [];
          active = -1;
          this.expanded = 'false';
          this.activeDescendant = null;
        },
        // Typing is a new name until an option is picked.
        onInput() {
          idInput.value = '';
          this.syncRole();
          this.cancel();
          if (nameInput.value.trim().length < SUGGEST_MIN) {
            this.close();
            return;
          }
          timer = setTimeout(() => this.search(), SUGGEST_DELAY);
        },
        // Another kind means other collectives: the pick no longer holds and the list is asked again.
        onKindChange() {
          idInput.value = '';
          this.syncRole();
          this.close();
          if (nameInput.value.trim().length >= SUGGEST_MIN) this.search();
        },
        async search() {
          this.cancel();
          const controller = new AbortController();
          request = controller;
          const params = new URLSearchParams({ q: nameInput.value.trim(), tipo: this.kind() });
          try {
            const response = await fetch(SUGGEST_URL + '?' + params, {
              headers: { Accept: 'text/html' },
              signal: controller.signal,
            });
            if (!response.ok) throw new Error('suggestions ' + response.status);
            const html = await response.text();
            if (controller.signal.aborted) return;
            request = null;
            box.innerHTML = html;
            options = Array.from(box.querySelectorAll('[role="option"]'));
            active = -1;
            this.activeDescendant = null;
            this.expanded = options.length > 0 ? 'true' : 'false';
          } catch {
            if (controller.signal.aborted) return;
            request = null;
            this.close();
          }
        },

        setActive(index) {
          active = index;
          options.forEach((option, i) => {
            option.classList.toggle('is-active', i === index);
            option.setAttribute('aria-selected', i === index ? 'true' : 'false');
          });
          this.activeDescendant = index >= 0 ? options[index].id : null;
          if (index >= 0) options[index].scrollIntoView({ block: 'nearest' });
        },
        pick(button) {
          idInput.value = button.dataset.collectiveId;
          nameInput.value = button.dataset.collectiveName;
          this.close();
          this.syncRole();
        },
        onKeydown(event) {
          if (event.isComposing) return;
          const open = this.expanded === 'true';
          switch (event.key) {
            case 'ArrowDown':
            case 'ArrowUp':
              event.preventDefault();
              if (!open) {
                if (event.key === 'ArrowDown' && nameInput.value.trim().length >= SUGGEST_MIN) this.search();
                return;
              }
              if (event.key === 'ArrowDown') this.setActive((active + 1) % options.length);
              else this.setActive(active <= 0 ? options.length - 1 : active - 1);
              break;
            case 'Enter':
              // With an option highlighted Enter picks it; otherwise it submits the form as usual.
              if (open && active >= 0) {
                event.preventDefault();
                this.pick(options[active].querySelector('button'));
              }
              break;
            case 'Escape':
              if (open) {
                event.preventDefault();
                this.close();
              }
              break;
          }
        },
        // The mouse never takes focus from the input: a blur would close the list before the click lands.
        onListMousedown(event) {
          if (event.target.closest('#collective-suggestions-box button')) event.preventDefault();
        },
        onListClick(event) {
          const button = event.target.closest('#collective-suggestions-box button[data-collective-id]');
          if (button) this.pick(button);
        },
      };
    });

    // The widgets of the event form (partials/event-fields). Each wraps the inputs it drives and adds no field: the
    // posted names and values are the ones the plain form posts. What a widget adds to the markup is x-cloak, so
    // without this script the form is as the server renders it.

    // Date picker: a month popup (role=dialog) over #date, opened by the [▦] button. The root carries data-today (today
    // in APP_TZ, AAAA-MM-DD) and, editing, data-keep (the event's own day, which stays valid when it is past). The
    // script fills .date-picker__grid with the calendar's own table (.calendar__grid, role=grid) of day buttons; the
    // cursor day is the one in the tab order, so the arrows move it and focus follows. A past day is aria-disabled,
    // not disabled: the arrows still walk over it and a screen reader says it is unavailable. Choosing a day writes
    // dd/mm/aaaa into the input; typing in the input still works and the popup follows it.
    Alpine.data('datePicker', () => {
      let input;
      let grid;
      let today;
      let keep;
      let cursor = '';

      const isPast = (iso) => iso < today && iso !== keep;

      return {
        closed: true,
        expanded: 'false',
        title: '',

        init() {
          const root = this.$root;
          input = root.querySelector('#date');
          grid = root.querySelector('.date-picker__grid');
          today = root.dataset.today;
          keep = dayOf(root.dataset.keep || '') ? root.dataset.keep : '';
        },

        // Draws the month of the cursor.
        render() {
          const month = cursor.slice(0, 8);
          const first = dayOf(month + '01');
          const selected = isoFromText(input.value);
          const name = MONTHS[first.getUTCMonth()] + ' de ' + first.getUTCFullYear();
          this.title = name;

          const table = document.createElement('table');
          table.className = 'calendar__grid';
          table.setAttribute('role', 'grid');
          table.setAttribute('aria-label', name);
          const headRow = table.createTHead().insertRow();
          for (const [short, full] of WEEKDAYS) {
            const th = document.createElement('th');
            th.className = 'calendar__weekday';
            th.setAttribute('scope', 'col');
            th.setAttribute('abbr', full);
            th.textContent = short;
            headRow.append(th);
          }
          const body = table.createTBody();
          let row = body.insertRow();
          for (let i = 0; i < first.getUTCDay(); i++) row.insertCell();
          for (let day = 1; day <= daysIn(first); day++) {
            if (row.cells.length === 7) row = body.insertRow();
            const iso = month + pad2(day);
            const cell = row.insertCell();
            const button = document.createElement('button');
            button.type = 'button';
            button.className = 'date-picker__day';
            button.dataset.day = iso;
            button.tabIndex = iso === cursor ? 0 : -1;
            button.textContent = String(day);
            button.setAttribute('aria-label', day + ' de ' + name);
            if (iso === today) button.setAttribute('aria-current', 'date');
            if (isPast(iso)) {
              button.classList.add('is-past');
              button.setAttribute('aria-disabled', 'true');
            }
            if (iso === selected) cell.setAttribute('aria-selected', 'true');
            cell.append(button);
          }
          grid.replaceChildren(table);
        },
        // Moves the cursor to a day, when it exists, and draws its month. With focus the day button takes the focus.
        go(next, focus) {
          if (!dayOf(next)) return;
          cursor = next;
          this.render();
          if (focus) grid.querySelector('[data-day="' + cursor + '"]').focus();
        },

        open() {
          const typed = isoFromText(input.value);
          cursor = typed && !isPast(typed) ? typed : today;
          this.render();
          this.closed = false;
          this.expanded = 'true';
          // The popup is hidden until Alpine has shown it, and a hidden button takes no focus.
          this.$nextTick(() => grid.querySelector('[data-day="' + cursor + '"]').focus());
        },
        // With focusInput the focus goes back to the date input, as when a day is chosen or Esc is pressed.
        close(focusInput) {
          this.closed = true;
          this.expanded = 'false';
          if (focusInput) input.focus();
        },
        toggle() {
          if (this.closed) this.open();
          else this.close(false);
        },
        choose(iso) {
          if (isPast(iso)) return;
          input.value = textFromIso(iso);
          this.close(true);
          // A script writes no change event: the form's own listeners (eventDynamic) hear this one, as when it is typed.
          input.dispatchEvent(new Event('change', { bubbles: true }));
        },

        onClick(event) {
          if (this.closed) return;
          const day = event.target.closest('[data-day]');
          if (day) {
            this.choose(day.dataset.day);
            return;
          }
          const nav = event.target.closest('[data-nav]');
          if (nav) this.go(addMonths(cursor, Number(nav.dataset.nav)), false);
        },
        // A click or a focus outside the widget closes the popup and leaves the focus where it went.
        onOutside(event) {
          if (!this.closed && !this.$root.contains(event.target)) this.close(false);
        },
        onKeydown(event) {
          if (event.isComposing || this.closed) return;
          if (event.key === 'Escape') {
            event.preventDefault();
            this.close(true);
            return;
          }
          // The keys below move the cursor, from a day of the grid. Enter and Space press the day button: onClick.
          if (!event.target.closest('.date-picker__grid')) return;
          const days = DAY_KEYS.get(event.key);
          let next;
          if (days !== undefined) next = addDays(cursor, days);
          else if (event.key === 'PageUp') next = addMonths(cursor, event.shiftKey ? -12 : -1);
          else if (event.key === 'PageDown') next = addMonths(cursor, event.shiftKey ? 12 : 1);
          else return;
          event.preventDefault();
          this.go(next, true);
        },
        // A day typed in the input moves the popup to its month.
        onInput() {
          const typed = isoFromText(input.value);
          if (!this.closed && typed) this.go(typed, false);
        },
      };
    });

    // Start time spinner: #time stays a text input posting HH:MM. The ▲▼ buttons (data-step, in minutes) and the keys
    // step it: Up and Down a minute, PageUp and PageDown an hour. The step counts total minutes, so 59 -> 00 adds an
    // hour, and it wraps at midnight without touching the date. An empty or unfinished time steps from 00:00. Typing is
    // masked (a colon after the hour) and a character that would make the time impossible is refused: the field keeps
    // the last valid text and .field-status says why.
    Alpine.data('timeSpinner', () => {
      let input;
      let last = '';

      return {
        message: '',

        init() {
          input = this.$root.querySelector('#time');
          last = input.value;
        },

        step(delta) {
          const minutes = (((clockMinutes(input.value) ?? 0) + delta) % 1440 + 1440) % 1440;
          input.value = clockText(minutes);
          last = input.value;
          this.message = '';
        },
        onClick(event) {
          const button = event.target.closest('button[data-step]');
          if (button) this.step(Number(button.dataset.step));
        },
        onKeydown(event) {
          const delta = CLOCK_KEYS.get(event.key);
          if (event.isComposing || event.target !== input || delta === undefined) return;
          event.preventDefault();
          this.step(delta);
        },
        onInput(event) {
          if (event.isComposing) return;
          const typed = typedClock(input.value, (event.inputType || '').startsWith('insert'));
          if (typed.error) {
            input.value = last;
            this.message = typed.error;
            return;
          }
          if (input.value !== typed.text) input.value = typed.text;
          last = typed.text;
          this.message = '';
        },
        // 9:30 is a time the server reads; HH:MM is how the field shows it.
        onChange() {
          const minutes = clockMinutes(input.value);
          if (minutes === null) return;
          input.value = clockText(minutes);
          last = input.value;
        },
      };
    });

    // Duration spinners: #duration_hours (0 to 23) and #duration_minutes (0 to 59) stay number inputs. The ▲▼ buttons
    // (data-step, in minutes) and the Up and Down keys step the whole duration: an hour on the hours field, a minute on
    // the minutes field, 59 -> 00 carrying into the hours. It stays between 15 minutes and 23h59: stepping past either
    // end stops there and .field-status says so. A typed value out of range is refused (the field keeps its last valid
    // value), and a duration under 15 minutes is set to 0h15 once the person leaves a field with both filled in.
    Alpine.data('durationSpinner', () => {
      let hours;
      let minutes;
      const last = new Map();

      const total = () => (Number(hours.value) || 0) * 60 + (Number(minutes.value) || 0);

      return {
        message: '',

        init() {
          hours = this.$root.querySelector('#duration_hours');
          minutes = this.$root.querySelector('#duration_minutes');
          last.set(hours, hours.value);
          last.set(minutes, minutes.value);
        },

        write(span) {
          hours.value = String(Math.floor(span / 60));
          minutes.value = pad2(span % 60);
          last.set(hours, hours.value);
          last.set(minutes, minutes.value);
        },
        step(delta) {
          const wanted = total() + delta;
          this.message = '';
          if (delta < 0 && wanted < DURATION_MIN) this.message = MSG_DURATION_MIN;
          if (delta > 0 && wanted > DURATION_MAX) this.message = MSG_DURATION_MAX;
          this.write(Math.min(Math.max(wanted, DURATION_MIN), DURATION_MAX));
        },
        onClick(event) {
          const button = event.target.closest('button[data-step]');
          if (button) this.step(Number(button.dataset.step));
        },
        onKeydown(event) {
          const sign = ARROW_KEYS.get(event.key);
          const unit = event.target === hours ? 60 : event.target === minutes ? 1 : 0;
          if (event.isComposing || sign === undefined || unit === 0) return;
          event.preventDefault();
          this.step(sign * unit);
        },
        onInput(event) {
          const field = event.target;
          const valid = !field.validity.badInput && /^\d{0,2}$/.test(field.value) && Number(field.value) <= Number(field.max);
          if (!valid) {
            field.value = last.get(field);
            this.message = field === hours ? MSG_HOURS : MSG_MINUTES;
            return;
          }
          last.set(field, field.value);
          this.message = '';
        },
        onChange() {
          if (hours.value === '' || minutes.value === '' || total() >= DURATION_MIN) return;
          this.write(DURATION_MIN);
          this.message = MSG_DURATION_MIN;
        },
      };
    });

    // Price: #price takes digits as cents and shows them as BRL (Intl.NumberFormat pt-BR: 2550 -> 25,50), never above
    // R$ 99.999,99 (a digit that would pass it is refused, with a .field-status message). The price posts as the server
    // reads it: 25,50 or 1.250,00. The Free checkbox (#free) empties and disables the price; on load the state follows
    // the checkbox. A disabled price is not posted, and the server ignores the price of a Free event anyway.
    Alpine.data('moneyInput', () => {
      let price;
      let free;
      let last = '';

      return {
        message: '',

        init() {
          const root = this.$root;
          price = root.querySelector('#price');
          free = root.querySelector('#free');
          root.querySelector('#dica-price').textContent = HINT_PRICE;
          const cents = centsFromText(price.value);
          if (cents > 0) price.value = formatCents(cents);
          last = price.value;
          this.syncFree();
        },

        syncFree() {
          if (free.checked) {
            price.value = '';
            last = '';
          }
          price.disabled = free.checked;
          this.message = '';
        },
        onInput(event) {
          if (event.target !== price) return;
          const cents = Number(price.value.replace(/\D/g, ''));
          if (cents > PRICE_MAX) {
            price.value = last;
            this.message = MSG_PRICE_MAX;
            return;
          }
          last = formatCents(cents);
          if (price.value !== last) price.value = last;
          this.message = '';
        },
        onChange(event) {
          if (event.target === free) this.syncFree();
        },
      };
    });

    // The dynamic event form: the cascades, the "Outros" fields and the "Analisar Data" panel. The root is the page's
    // .event-layout (pages/app/evento-form), around the form and the panel (aside.event-aside), so it hears the change and
    // the input of every field, told apart by event.target.id, and the widgets above sit inside it. It adds no field: what
    // the form posts is what the plain form posts.
    //  - A new state fetches the cities and a new style the substyles (the select-options of /opcoes/cidades and
    //    /opcoes/subestilos). If the fetch fails the list stays as it was and ATUALIZAR LISTAS and its hint show again.
    //  - "qual tipo?" and "qual estilo?" show for Outros, and on load also when they hold a value or an error (a refused
    //    form), until the person changes their select. A hidden field keeps its value. The style Outros turns the
    //    substyle off.
    //  - ANALISAR DATA, enabled for a date the server would read, loads the day list into the panel with htmx. The panel
    //    says when the date, the state or the city has changed since: a note, not a reload, so the live region stays
    //    quiet while the person types.
    Alpine.data('eventDynamic', () => {
      let typeOther;
      let styleOther;
      let citySelect;
      let substyleSelect;

      return {
        typeValue: '',
        styleValue: '',
        stateValue: '',
        cityValue: '',
        dateValue: '',
        typeKept: false,
        styleKept: false,
        citiesFailed: false,
        substylesFailed: false,
        analysedUrl: '',

        init() {
          const root = this.$root;
          typeOther = root.querySelector('#type_other');
          styleOther = root.querySelector('#style_other');
          citySelect = root.querySelector('#city');
          substyleSelect = root.querySelector('#substyle');
          this.typeValue = root.querySelector('#type').value;
          this.styleValue = root.querySelector('#style').value;
          this.stateValue = root.querySelector('#state').value;
          this.cityValue = citySelect.value;
          this.dateValue = root.querySelector('#date').value;
          this.typeKept = typeOther.value !== '' || typeOther.hasAttribute('aria-invalid');
          this.styleKept = styleOther.value !== '' || styleOther.hasAttribute('aria-invalid');
        },

        get typeOtherShown() {
          return this.typeValue === OTHER || this.typeKept;
        },
        get styleOtherShown() {
          return this.styleValue === OTHER || this.styleKept;
        },
        get substyleOff() {
          return this.styleValue === OTHER;
        },
        // Only a list that could not be refilled brings the button back.
        get refreshShown() {
          return this.citiesFailed || this.substylesFailed;
        },
        get analyseDisabled() {
          return this.dayUrl() === '';
        },
        get panelIdle() {
          return this.analysedUrl === '';
        },
        get panelStale() {
          return this.analysedUrl !== '' && this.analysedUrl !== this.dayUrl();
        },

        // The day list's URL for the form as it is now, or '' when the date is not one. The events of the form's city
        // are highlighted only when both the state and the city are chosen.
        dayUrl() {
          const day = dayFromField(this.dateValue);
          if (day === '') return '';
          const params = new URLSearchParams([['data', day]]);
          if (this.stateValue !== '' && this.cityValue !== '') {
            params.set('destaque_uf', this.stateValue);
            params.set('destaque_cidade', this.cityValue);
          }
          return DAY_URL + '?' + params;
        },
        analyse() {
          const url = this.dayUrl();
          if (url === '') return;
          this.analysedUrl = url;
          // A request that fails is reported in the panel by the htmx:sendError listener below.
          htmx.ajax('GET', url, { target: '#' + RESULT_ID, swap: 'innerHTML' }).catch(() => {});
        },

        async refreshCities() {
          const done = await refill(citySelect, CITIES_URL, [['uf', this.stateValue], ['selecionada', citySelect.value]]);
          if (done === null) return;
          this.citiesFailed = !done;
          this.cityValue = citySelect.value;
        },
        async refreshSubstyles() {
          const done = await refill(substyleSelect, SUBSTYLES_URL, [['estilo', this.styleValue], ['selecionada', substyleSelect.value]]);
          if (done === null) return;
          this.substylesFailed = !done;
        },

        onFieldChange(event) {
          const field = event.target;
          switch (field.id) {
            case 'type':
              this.typeValue = field.value;
              this.typeKept = false;
              break;
            case 'style':
              this.styleValue = field.value;
              this.styleKept = false;
              this.refreshSubstyles();
              break;
            case 'state':
              this.stateValue = field.value;
              this.refreshCities();
              break;
            case 'city':
              this.cityValue = field.value;
              break;
            case 'date':
              this.dateValue = field.value;
              break;
          }
        },
        // The date is followed as it is typed, so the button and the note keep up with it.
        onFieldInput(event) {
          if (event.target.id === 'date') this.dateValue = event.target.value;
        },
      };
    });

    // The calendar page (pages/app/home): the month as an app, over markup that already works as plain links. The root
    // (the x-data element, which init marks calendar-page--js) wraps the month (#calendar-month) and the sheet
    // (<dialog id="evento-sheet">), so the listeners are delegated and the HTML a swap brings needs no setup. Only a plain left click is taken: a modifier key, the
    // middle button, no script or a request that fails all follow the link instead.
    //  - A chip loads the event (the HTMX answer of GET /eventos/:id) into the sheet and opens it. Closing it, by Esc or
    //    [✕], gives the focus back to the chip, or to the same event's chip in the month that replaced it, or to the day
    //    that is the tab stop.
    //  - The month bar and the filter swap #calendar-month for GET /calendario with their own query, push the URL of
    //    that page and slide the month (a view transition) toward the side it moves. Back and forward swap again.
    //  - The day links are one tab stop (roving tabindex); the arrows, Home and End move between them.
    //  - Below 640px the number of a day loads that day's list into #calendar-day instead of leaving the page.
    // The class calendar-page--js, which the script puts on its root, hides the FILTRAR button: the filter applies on change.
    //
    // Two actions can come within one response time, and the person's choice must survive that. Every month change is
    // built from latest(), the newest address asked for (the month that is out or waiting, else the one in the page),
    // never from the month in the page, whose filter, month bar and day links are stale while a newer month loads. Of
    // each kind of request (the month, the event of a chip, a day list) one runs at a time and the newest waits behind it
    // (newestOnly), and each kind names the element it makes its requests from (the slot, the sheet, the root), because
    // htmx keeps one request slot for each such element and queues a second request on it, answering its promise at
    // once. A month that a newer one supersedes is swapped in if it came, but sets nothing up.
    Alpine.data('calendarPage', () => {
      let root;
      let opener = null; // the chip the open sheet came from
      let slideTimer = 0;
      let onPopState;
      let loadMonth; // the runners of the three kinds of request, made by init
      let loadSheet;
      let loadDay;
      let dayAfter = ''; // a phone's day tapped while a month is out, which the month that lands opens

      const monthOf = () => root.querySelector('#calendar-month');
      const dayLinks = () => [...monthOf().querySelectorAll('.calendar__daynum')];
      const dayLink = (key) => monthOf().querySelector('[data-day="' + key + '"] .calendar__daynum');

      // The address of a month's page, as the server's own links have it: the empty choices are left out and the rest is
      // in order.
      function hrefOf({ mes, uf, cidade }) {
        const params = new URLSearchParams([['mes', mes], ['uf', uf], ['cidade', cidade]].filter(([, value]) => value !== ''));
        params.sort();
        return '/?' + params;
      }

      // The address a month in the page stands for.
      const queryOf = (month) => ({
        mes: month.dataset.month,
        uf: month.querySelector('#calendar-uf').value,
        cidade: month.querySelector('#calendar-cidade').value,
      });
      // The newest address asked for: where every month change starts from.
      const latest = () => loadMonth.newest() ?? queryOf(monthOf());
      // Today's month, as the server's Hoje link has it (the time zone is the server's, not the browser's).
      const todayOf = () => new URL(monthOf().querySelector('.calendar__today').href).searchParams.get('mes');

      // Of the day links only one is in the tab order, and the arrow keys move it.
      function rove(stop) {
        for (const link of dayLinks()) link.setAttribute('tabindex', link === stop ? '0' : '-1');
      }

      // A selector for a control of the month, so the focus can go to its twin in the next month.
      function twin(control) {
        if (control.id) return '#' + control.id;
        if (control.matches('a[rel]')) return 'a[rel="' + control.getAttribute('rel') + '"]';
        return control.matches('.calendar__today') ? '.calendar__today' : '';
      }

      return {
        init() {
          root = this.$root;
          root.classList.add('calendar-page--js');
          this.enhance();
          // What the page does when a request hangs: the month goes to its address, which already is the history entry the
          // choice made, and the others to their links.
          loadMonth = newestOnly((query, newer) => this.showMonth(query, newer), (query) => location.replace(hrefOf(query)));
          loadSheet = newestOnly((chip, newer) => this.showEvent(chip, newer), (chip) => location.assign(chip.href));
          loadDay = newestOnly((key, newer) => this.showDay(key, newer), (key) => {
            const link = dayLink(key);
            if (link) location.assign(link.href);
          });
          onPopState = () => {
            if (location.pathname !== '/') return;
            const sheet = root.querySelector('#evento-sheet');
            if (sheet.open) sheet.close();
            const params = new URLSearchParams(location.search);
            const mes = params.get('mes') ?? '';
            this.goTo({ mes: dayOf(mes + '-01') ? mes : todayOf(), uf: params.get('uf') ?? '', cidade: params.get('cidade') ?? '' }, false);
          };
          window.addEventListener('popstate', onPopState);
        },
        destroy() {
          window.removeEventListener('popstate', onPopState);
        },

        // What the script gives the month in the page, at the start and after each swap: the tab stop of the day links
        // (the chosen day, else today, else the first day of the month) and the entrance of the chips.
        enhance() {
          const month = monthOf();
          const stop =
            month.querySelector('.calendar__cell--chosen .calendar__daynum') ||
            month.querySelector('.calendar__cell[aria-current="date"] .calendar__daynum') ||
            month.querySelector('.calendar__cell:not(.calendar__cell--out) .calendar__daynum');
          rove(stop || dayLinks()[0]);
          stagger(month.querySelectorAll('.calendar__chip'));
        },

        // The person's choice of a month (query: mes, uf, cidade): its address is the history entry at once, one for each
        // choice, and the month loads behind whatever month is out. push is false when the address already is the
        // entry (back and forward).
        goTo(query, push = true) {
          const href = hrefOf(query);
          if (push && href !== location.pathname + location.search) history.pushState(null, '', href);
          loadMonth(query);
        },

        // Swaps the month of query for the one GET /calendario answers alone, into #calendar-slot (the wrapper that holds
        // only the month). A month that was not swapped in (an error, no answer) leaves the old one in the page, and the
        // address is loaded as a page instead; so does the newest month that is out after HANG_WAIT. When a newer month
        // waits behind this one, this one sets nothing up (the tab stop, the stagger, the status are the newest month's),
        // and a failure of it is the newer one's to meet.
        async showMonth(query, newer) {
          const old = monthOf();
          const slot = root.querySelector('#calendar-slot');
          const href = hrefOf(query);
          const from = old.dataset.month;
          const slide = query.mes === from ? '' : query.mes > from ? 'next' : 'prev';
          // No slide under the open sheet: the page behind it is not seen.
          const animate =
            slide !== '' &&
            typeof document.startViewTransition === 'function' &&
            !matchMedia(REDUCED_MOTION).matches &&
            !root.querySelector('#evento-sheet').open;

          // The answer is about to be swapped in, which is the moment to read what the person is on and to tell the
          // stylesheet the direction: the request may take longer than the attribute lives, and a month that comes later
          // must not overwrite the direction of one that is still sliding. The timer starts here, and nothing else clears
          // the attribute (the swap comes inside the transition, before its pseudo-elements are styled).
          let held = false;
          let focused = '';
          const listening = new AbortController();
          slot.addEventListener('htmx:beforeSwap', (event) => {
            if (event.detail.target !== slot) return;
            held = old.contains(document.activeElement);
            focused = held ? twin(document.activeElement) : '';
            if (!animate) return;
            clearTimeout(slideTimer);
            document.documentElement.dataset.calendarSlide = slide;
            slideTimer = setTimeout(() => delete document.documentElement.dataset.calendarSlide, SLIDE_KEEP);
          }, { signal: listening.signal });
          let done;
          try {
            done = await swapped(
              () => htmx.ajax('GET', MONTH_URL + href.slice(1), { source: slot, target: slot, swap: animate ? 'innerHTML transition:true' : 'innerHTML' }),
              slot,
            );
          } finally {
            listening.abort();
          }
          const month = monthOf();
          // The swap took the control out of the page, so the focus goes to its twin before anything else (the person is
          // still on that control, whatever the month that comes next).
          if (focused) month?.querySelector(focused)?.focus();
          if (newer()) return;
          if (!done || !month || month === old) {
            location.replace(href);
            return;
          }

          this.enhance();
          // A control with no twin (a chip or a day, whose sheet closed while the month was out) leaves the focus on the
          // day that is the tab stop, not on the body.
          if (held && document.activeElement === document.body) month.querySelector('.calendar__daynum[tabindex="0"]')?.focus();
          root.querySelector('#calendar-status').textContent = month.querySelector('#calendar-title').textContent;
          // A day that a phone chose while this month was out (or whose list is out) is chosen again in the month that
          // came: that is the one with the state the person picked.
          const day = dayAfter || loadDay.newest();
          dayAfter = '';
          if (day) loadDay(day);
        },

        // Loads the event of a chip into the sheet and opens it. The body is emptied first, so what is in it afterwards
        // is this answer: no title means it failed (an error is not swapped in), and the event's own page opens instead.
        // Only the newest chip opens the sheet.
        async showEvent(chip, newer) {
          const dialog = root.querySelector('#evento-sheet');
          const body = dialog.querySelector('.sheet__body');
          body.replaceChildren();
          try {
            await htmx.ajax('GET', chip.getAttribute('href'), { source: dialog, target: body, swap: 'innerHTML' });
          } catch {
            // The title tells below.
          }
          if (newer()) return;
          const title = body.querySelector('h2.sheet__title');
          if (!title || !title.id) {
            location.assign(chip.href);
            return;
          }
          opener = chip;
          dialog.setAttribute('aria-labelledby', title.id);
          dialog.showModal();
        },
        // Esc, [✕] and close() all end here, so the focus goes back whichever of them closed the sheet: to the chip, or to
        // its twin (the chip of the same event) when a month swap replaced it, else to the day that is the tab stop.
        onSheetClose() {
          const href = opener?.getAttribute('href');
          const chip = opener?.isConnected
            ? opener
            : [...monthOf().querySelectorAll('.calendar__chip')].find((other) => other.getAttribute('href') === href);
          (chip || monthOf().querySelector('.calendar__daynum[tabindex="0"]'))?.focus();
          opener = null;
        },

        // A phone's choice of a day (its key, AAAA-MM-DD): marks it and loads its list into the panel below the grid. A
        // day that is not in the month (it changed under the choice) is dropped. The old list stays until the new one
        // comes, so one that is still the same element after the request means it failed; a panel that was swapped out of
        // the page with its month says nothing.
        async showDay(key, newer) {
          const link = dayLink(key);
          if (!link) return;
          const month = monthOf();
          const panel = month.querySelector('#calendar-day');
          const shown = panel.firstElementChild;
          for (const cell of month.querySelectorAll('.calendar__cell--chosen')) {
            cell.classList.remove('calendar__cell--chosen');
            cell.querySelector('.calendar__daynum').removeAttribute('aria-current');
          }
          link.closest('.calendar__cell').classList.add('calendar__cell--chosen');
          link.setAttribute('aria-current', 'true');
          rove(link);

          try {
            await htmx.ajax('GET', link.getAttribute('href'), { source: root, target: panel, swap: 'innerHTML' });
          } catch {
            // The list in the panel tells below.
          }
          if (!panel.isConnected || newer()) return;
          if (!panel.querySelector('#day-list') || panel.firstElementChild === shown) {
            location.assign(link.href);
            return;
          }
          stagger(panel.querySelectorAll('.item-list__item'));
          panel.scrollIntoView({ block: 'nearest' });
        },

        onCalendarClick(event) {
          if (event.defaultPrevented || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
          const link = event.target.closest('a');
          // The sheet is in the root too: its own links (edit, tickets) are plain links.
          if (!link || !monthOf().contains(link)) return;
          if (link.classList.contains('calendar__chip')) {
            event.preventDefault();
            loadSheet(link);
          } else if (link.closest('.calendar__nav')) {
            // A month from the newest one asked for, not from the one in the page: the link carries the latter.
            event.preventDefault();
            const asked = latest();
            const rel = link.getAttribute('rel');
            this.goTo({ ...asked, mes: rel ? addMonths(asked.mes + '-01', rel === 'next' ? 1 : -1).slice(0, 7) : todayOf() });
          } else if (link.classList.contains('calendar__daynum') && !matchMedia(WIDE).matches) {
            // While a month is out the day waits for it: its link carries the filter of the month in the page.
            event.preventDefault();
            const key = link.closest('.calendar__cell').dataset.day;
            if (loadMonth.newest()) dayAfter = key;
            else loadDay(key);
          } else if ((link.classList.contains('calendar__daynum') || link.classList.contains('calendar__more')) && loadMonth.newest()) {
            // A wider screen leaves the page for the day's list, and the filter it takes is the newest one asked for.
            event.preventDefault();
            const url = new URL(link.href);
            const asked = latest();
            for (const name of ['uf', 'cidade']) {
              if (asked[name]) url.searchParams.set(name, asked[name]);
              else url.searchParams.delete(name);
            }
            location.assign(url);
          }
        },

        // The filter applies on change. A new state drops the city (its list is of the old state).
        onCalendarChange(event) {
          const field = event.target;
          if (field.id !== 'calendar-uf' && field.id !== 'calendar-cidade') return;
          const asked = latest();
          this.goTo(field.id === 'calendar-uf' ? { ...asked, uf: field.value, cidade: '' } : { ...asked, cidade: field.value });
        },

        // Arrow keys, Home and End move between the day links: a day, a week, the start or the end of the week. Past the
        // grid they stay, and the days of the neighbouring months that the grid shows are reached like any other.
        onCalendarKeydown(event) {
          if (event.ctrlKey || event.metaKey || event.altKey || event.shiftKey) return;
          const link = event.target.closest('.calendar__daynum');
          if (!link || !monthOf().contains(link)) return;
          const links = dayLinks();
          const at = links.indexOf(link);
          let to;
          switch (event.key) {
            case 'ArrowLeft':
              to = at - 1;
              break;
            case 'ArrowRight':
              to = at + 1;
              break;
            case 'ArrowUp':
              to = at - 7;
              break;
            case 'ArrowDown':
              to = at + 7;
              break;
            case 'Home':
              to = at - (at % 7);
              break;
            case 'End':
              to = at - (at % 7) + 6;
              break;
            default:
              return;
          }
          event.preventDefault();
          if (to >= 0 && to < links.length) {
            rove(links[to]);
            links[to].focus();
          }
        },
      };
    });
  });

  // The "Na mesma data" panel of the event form (eventDynamic.analyse): htmx swaps the day list into #analise-resultado.
  const isPanelTarget = (target) => target instanceof Element && target.id === RESULT_ID;

  // htmx swaps no 4xx by default. A date the server refuses is a 422 whose [erro] line belongs in the panel, and only there.
  document.addEventListener('htmx:beforeSwap', (event) => {
    if (event.detail.xhr.status === 422 && isPanelTarget(event.detail.target)) {
      event.detail.shouldSwap = true;
      event.detail.isError = false;
    }
  });

  // The rows enter one after another: .animate-fade-up reads --i, which the CSSOM sets because a style attribute would
  // break the CSP. Under prefers-reduced-motion base.css cuts the animation short. The panel scrolls into view, for a
  // phone, where it is below the form.
  document.addEventListener('htmx:afterSwap', (event) => {
    const target = event.detail.target;
    if (!isPanelTarget(target)) return;
    stagger(target.querySelectorAll('.item-list__item'));
    target.closest('aside').scrollIntoView({ block: 'nearest' });
  });

  // An answer that is no list (a server error, or no answer) says so in the panel instead of leaving the old list.
  for (const name of ['htmx:responseError', 'htmx:sendError']) {
    document.addEventListener(name, (event) => {
      if (!isPanelTarget(event.detail.target)) return;
      const message = document.createElement('p');
      message.className = 'status status--error';
      message.setAttribute('role', 'alert');
      message.textContent = MSG_DAY_FAILED;
      event.detail.target.replaceChildren(message);
    });
  }
})();
