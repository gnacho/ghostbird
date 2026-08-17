  /* GhostBird landing: idioma, tema, count-up, FAQ, copiar y reveal */
(function () {
  'use strict';

  var LANG_KEY = 'ghostbird-lang';
  var THEME_KEY = 'ghostbird-theme';
  var root = document.documentElement;
  var reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  var dict = I18N.es;

  /* ---------- Idioma ---------- */
  function applyLang(lang) {
    dict = I18N[lang] || I18N.es;
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

  /* ---------- Tema: dark por defecto (Luminex), conmutable y persistente ---------- */
  var themeBtn = document.getElementById('themeBtn');
  var themeIcon = document.getElementById('themeIcon');

  function iconPath(t) {
    if (t === 'light') {
      return '<circle cx="12" cy="12" r="4"/><path d="M12 2v2"/><path d="M12 20v2"/><path d="m4.93 4.93 1.41 1.41"/><path d="m17.66 17.66 1.41 1.41"/><path d="M2 12h2"/><path d="M20 12h2"/><path d="m6.34 17.66-1.41 1.41"/><path d="m19.07 4.93-1.41 1.41"/>';
    }
    return '<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/>';
  }

  function themeNow() { return root.getAttribute('data-theme') || 'dark'; }

  function applyTheme(theme) {
    root.setAttribute('data-theme', theme);
    var meta = document.querySelector('meta[name="theme-color"]:not([media])') ||
      document.querySelector('meta[name="theme-color"]');
    var colors = { dark: '#0B1020', light: '#F1F4FB' };
    if (meta) meta.setAttribute('content', colors[theme] || colors.dark);
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
    return 'dark';
  }

  /* ---------- Count-up de las stats ---------- */
  function animateCount(el) {
    var target = parseInt(el.getAttribute('data-target'), 10);
    if (isNaN(target)) return;
    if (reduceMotion || !('requestAnimationFrame' in window)) {
      el.textContent = String(target);
      return;
    }
    var dur = 1100;
    var t0 = null;
    function step(ts) {
      if (t0 === null) t0 = ts;
      var p = Math.min((ts - t0) / dur, 1);
      var eased = 1 - Math.pow(1 - p, 3);
      el.textContent = String(Math.round(eased * target));
      if (p < 1) requestAnimationFrame(step);
    }
    requestAnimationFrame(step);
  }

  var statNums = Array.prototype.slice.call(document.querySelectorAll('.stat-num'));
  var statsDone = false;
  function runStats() {
    if (statsDone) return;
    statsDone = true;
    statNums.forEach(function (el) {
      el.textContent = '0';
      animateCount(el);
    });
  }
  if ('IntersectionObserver' in window) {
    var so = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          runStats();
          so.disconnect();
        }
      });
    }, { threshold: 0.35 });
    var statsBand = document.getElementById('stats');
    if (statsBand) so.observe(statsBand);
  } else {
    runStats();
  }

  /* ---------- FAQ acordeón ---------- */
  document.querySelectorAll('.faq-q').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var item = btn.closest('.faq-item');
      var open = item.classList.toggle('open');
      btn.setAttribute('aria-expanded', open ? 'true' : 'false');
    });
  });

  /* ---------- Botones de copiar (con fallback para HTTP) ---------- */
  function labelOf(btn) {
    return btn.querySelector('.copy-label');
  }

  document.querySelectorAll('.copy-btn').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var text = btn.getAttribute('data-copy') || '';
      var done = function () {
        var lab = labelOf(btn);
        if (lab) {
          lab.textContent = dict['misc.copied'] || 'OK';
          btn.classList.add('copied');
          setTimeout(function () {
            lab.textContent = dict['inst.copy'] || 'Copy';
            btn.classList.remove('copied');
          }, 1600);
        }
      };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(done, function () {
          legacyCopy(text);
          done();
        });
      } else {
        legacyCopy(text);
        done();
      }
    });
  });

  function legacyCopy(text) {
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.left = '-9999px';
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand('copy'); } catch (e) { /* noop */ }
    document.body.removeChild(ta);
  }

  /* ---------- Sistema unificado de entrada (Premium, un solo sistema) ----------
     [data-reveal] entra con translateY + opacity; los hijos de un
     [data-reveal-group] reciben --i para el stagger (calc(--i * --stagger)).
     Los grupos con [data-reveal-load] (hero) entran al cargar la página;
     el resto, al cruzar el viewport (una sola activación por elemento). */
  var rvAll = Array.prototype.slice.call(document.querySelectorAll('[data-reveal]'));
  rvAll.forEach(function (el) {
    el.classList.add('rv');
    if (el.hasAttribute('data-reveal-soft')) el.classList.add('rv-soft');
  });

  document.querySelectorAll('[data-reveal-group]').forEach(function (group) {
    var items = group.querySelectorAll('[data-reveal]');
    Array.prototype.forEach.call(items, function (el, i) {
      el.style.setProperty('--i', String(i));
    });
  });

  function activate(el) {
    el.classList.add('in');
    var finish = function () { el.classList.add('done'); };
    if (reduceMotion) { finish(); return; }
    el.addEventListener('transitionend', function onEnd(ev) {
      if (ev.target !== el) return;
      if (ev.propertyName === 'transform' || ev.propertyName === 'opacity') {
        el.removeEventListener('transitionend', onEnd);
        finish();
      }
    });
    /* Red de seguridad: la entrada más larga acaba en 820ms */
    setTimeout(finish, 1300);
  }

  var loadReveals = [];
  var ioReveals = [];
  rvAll.forEach(function (el) {
    var g = el.closest('[data-reveal-group]');
    if (g && g.hasAttribute('data-reveal-load')) loadReveals.push(el);
    else ioReveals.push(el);
  });

  if (!('IntersectionObserver' in window) || reduceMotion) {
    rvAll.forEach(activate);
  } else {
    /* Hero: dos rAF para pintar un frame en estado inicial y que la
       transición de entrada arranque de verdad */
    loadReveals.forEach(function (el) {
      requestAnimationFrame(function () {
        requestAnimationFrame(function () { activate(el); });
      });
    });
    var rvo = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          activate(entry.target);
          rvo.unobserve(entry.target);
        }
      });
    }, { threshold: 0.12, rootMargin: '0px 0px -40px 0px' });
    ioReveals.forEach(function (el) { rvo.observe(el); });
  }

  /* ---------- Arranque ---------- */
  applyTheme(initialTheme());
  applyLang(initialLang());
})();
