// Alpine CSP build: it has no expression evaluator, so a template attribute may only name a property or
// method registered here with Alpine.data(): x-data="shell", @click="toggle", :class="sidebarClass".
// No negation, ternary, string literal or call arguments in the template; put that logic in a getter or method.
(() => {
  'use strict';

  // The sidebar is a column from this width and a drawer below it (components.css).
  const DESKTOP = '(min-width: 1024px)';

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
  });
})();
