/* GhostBird datasheet: idioma, tema y reveal */
(function () {
  'use strict';

  var LANG_KEY = 'ghostbird-lang';
  var THEME_KEY = 'ghostbird-theme';
  var root = document.documentElement;
  var reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  /* ---------- Idioma ---------- */
  function applyLang(lang) {
    var dict = I18N[lang] || I18N.es;
    document.querySelectorAll('[data-i18n]').forEach(function (el) {
      var key = el.getAttribute('data-i18n');
      if (dict[key]) el.textContent = dict[key];
    });
    document.querySelectorAll('[data-i18n-aria]').forEach(function (el) {
      var key = el.getAttribute('data-i18n-aria');
      if (dict[key]) el.setAttribute('aria-label', dict[key]);
    });
    root.lang = lang;
    if (dict['meta.title']) document.title = dict['meta.title'];
    var metaDesc = document.querySelector('meta[name="description"]');
    if (metaDesc && dict['meta.desc']) metaDesc.setAttribute('content', dict['meta.desc']);
    var ogTitle = document.querySelector('meta[property="og:title"]');
    if (ogTitle && dict['meta.title']) ogTitle.setAttribute('content', dict['meta.title']);
    var ogDesc = document.querySelector('meta[property="og:description"]');
    if (ogDesc && dict['meta.desc']) ogDesc.setAttribute('content', dict['meta.desc']);
    var twTitle = document.querySelector('meta[name="twitter:title"]');
    if (twTitle && dict['meta.title']) twTitle.setAttribute('content', dict['meta.title']);
    var twDesc = document.querySelector('meta[name="twitter:description"]');
    if (twDesc && dict['meta.desc']) twDesc.setAttribute('content', dict['meta.desc']);
    var sel = document.getElementById('langSelect');
    if (sel) sel.value = lang;
    try { localStorage.setItem(LANG_KEY, lang); } catch (e) { /* noop */ }
  }

  function initialLang() {
    var qs = new URLSearchParams(window.location.search).get('hl');
    if (qs === 'es' || qs === 'en') return qs;
    try {
      var saved = localStorage.getItem(LANG_KEY);
      if (saved === 'es' || saved === 'en') return saved;
    } catch (e) { /* noop */ }
    var nav = (navigator.language || '').toLowerCase();
    if (nav.indexOf('es') === 0) return 'es';
    if (nav.indexOf('en') === 0) return 'en';
    return 'en';
  }

  var langSelect = document.getElementById('langSelect');
  if (langSelect) {
    langSelect.addEventListener('change', function () {
      applyLang(this.value);
      try {
        var u = new URL(window.location.href);
        u.searchParams.set('hl', this.value);
        window.history.replaceState(null, '', u);
      } catch (e) { /* noop */ }
    });
  }

  /* ---------- Tema: claro u oscuro, sigue el sistema si no hay preferencia ---------- */
  var themeBtn = document.getElementById('themeBtn');
  var themeIcon = document.getElementById('themeIcon');

  function iconPath(t) {
    if (t === 'dark') {
      return '<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/>';
    }
    return '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/>';
  }

  function systemTheme() {
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }

  function themeNow() { return root.getAttribute('data-theme') || 'light'; }

  function applyTheme(theme) {
    root.setAttribute('data-theme', theme);
    var meta = document.querySelector('meta[name="theme-color"]:not([media])') ||
      document.querySelector('meta[name="theme-color"]');
    var colors = { light: '#f4f3ef', dark: '#14161a' };
    if (meta) meta.setAttribute('content', colors[theme] || colors.light);
    try { localStorage.setItem(THEME_KEY, theme); } catch (e) { /* noop */ }
    if (themeIcon) themeIcon.innerHTML = iconPath(theme);
  }

  if (themeBtn) {
    themeBtn.addEventListener('click', function () {
      applyTheme(themeNow() === 'dark' ? 'light' : 'dark');
    });
  }

  function initialTheme() {
    try {
      var saved = localStorage.getItem(THEME_KEY);
      if (saved === 'light' || saved === 'dark') return saved;
    } catch (e) { /* noop */ }
    return systemTheme();
  }

  /* ---------- Reveal sutil al hacer scroll ---------- */
  var reveals = Array.prototype.slice.call(
    document.querySelectorAll('.titleblock, .sec, .footer')
  );
  reveals.forEach(function (r) { r.classList.add('reveal'); });
  if ('IntersectionObserver' in window && !reduceMotion) {
    var ro = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          entry.target.classList.add('in');
          ro.unobserve(entry.target);
        }
      });
    }, { threshold: 0.08, rootMargin: '0px 0px -40px 0px' });
    reveals.forEach(function (r) { ro.observe(r); });
  } else {
    reveals.forEach(function (r) { r.classList.add('in'); });
  }

  /* ---------- Arranque ---------- */
  applyTheme(initialTheme());
  applyLang(initialLang());
})();
