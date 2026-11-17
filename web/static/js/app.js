// Alpine CSP build: it has no expression evaluator, so a template attribute may only name a property or
// method registered here with Alpine.data(): x-data="shell", @click="toggle", :class="sidebarClass".
// No negation, ternary, string literal or call arguments in the template; put that logic in a getter or method.
// Every component here only enhances server-rendered markup: without it the pages still work.
(() => {
  'use strict';

  // The sidebar is a column from this width and a drawer below it (components.css).
  const DESKTOP = '(min-width: 1024px)';

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

      return {
        open: false,
        get expanded() {
          return this.open ? 'true' : 'false';
        },
        get sidebarClass() {
          return this.open ? 'is-open' : '';
        },
        get toggleLabel() {
          return this.open ? '[✕] fechar' : '[≡] menu';
        },
        toggle() {
          this.open = !this.open;
        },
        close() {
          if (!this.open) return;
          this.open = false;
          this.$refs.toggle.focus();
        },
        // From 1024px the toggle is hidden, so close without moving focus to it.
        init() {
          query = window.matchMedia(DESKTOP);
          onBreakpoint = (event) => {
            if (event.matches) this.open = false;
          };
          query.addEventListener('change', onBreakpoint);
        },
        destroy() {
          query.removeEventListener('change', onBreakpoint);
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
    // script fills .date-picker__grid with a table[role=grid] of day buttons; the cursor day is the one in the tab
    // order, so the arrows move it and focus follows. A past day is aria-disabled, not disabled: the arrows still walk
    // over it and a screen reader says it is unavailable. Choosing a day writes dd/mm/aaaa into the input; typing in the
    // input still works and the popup follows it.
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
          today = dayOf(root.dataset.today || '') ? root.dataset.today : isoOf(new Date());
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
          table.className = 'date-picker__table';
          table.setAttribute('role', 'grid');
          table.setAttribute('aria-label', name);
          const headRow = table.createTHead().insertRow();
          for (const [short, full] of WEEKDAYS) {
            const th = document.createElement('th');
            th.className = 'date-picker__weekday';
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
  });
})();
