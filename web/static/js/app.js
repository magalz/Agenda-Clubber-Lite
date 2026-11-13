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
    'nome novo: quem cria um coletivo novo entra como dono. para entrar como membro, escolha um já cadastrado na lista';

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
    // collective, whose creator is its owner: the script then selects Dono and disables Membro.
    Alpine.data('collectivePicker', () => {
      let nameInput;
      let idInput;
      let box;
      let roleHint;
      let hintDefault;
      let owner;
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
          roleHint = root.querySelector('#role-hint');
          hintDefault = roleHint.textContent;
          owner = root.querySelector('input[name="role"][value="owner"]');
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
  });
})();
