# DESIGN.md — Landing GhostBird (ghostbird.cloudless.club)

Fuente de verdad visual de la landing. Si CSS y este fichero divergen, manda este fichero.

## Contexto (Phase 0)

- **Artefacto**: landing de producto self-hosted para audiencia técnica (personas
  que autoalojan Ghost). Acción principal: instalar; secundarias: repo, club, Ko-fi.
- **Dirección decidida por el propietario (17-Ago-2026)**: cambiar el datasheet
  industrial por un tema basado en **Luminex** (ThemeForest, Rometheme):
  dark-tech, glass cards, gradientes suaves, marquee. Adaptación, no copia.
- **Descartado de Luminex**: team, blog/news, pricing, newsletter, multi-página.
- **Posicionamiento**: técnico, honesto, sereno. Sin hype.

## Qué se tomó de Luminex y cómo se adaptó

| Elemento Luminex | Adaptación GhostBird |
|---|---|
| Nav sticky flotante | Pill glass flotante (sticky top 10px) con logo pájaro, anclas y CTA pill "Instalar" |
| Hero con eyebrow pill + headline con palabra gradiente + doble CTA + visual | Mockup del dashboard de Ghost reconstruido en HTML/CSS glass: tarjeta con KPI, sparkline SVG y mini-stats, más tarjeta terminal con `systemctl status`. Nada de imagen 3D ni stock |
| Logo marquee de clientes | Marquee de "protocol facts" (chips glass con icono Lucide): NDJSON, JWT HS256, 13 pipes, SQLite WAL, 0 telemetría, ~11 MB, backups, /metrics |
| "A strong foundation" features | Grid 2x2 de feature cards glass: Drop-in fiel / Sesiones agregadas / Métricas honestas / Operable por una persona |
| Split texto+visual | "Ghost ni se entera": texto a la izquierda, config tinybird JSON en card glass oscura con sintaxis coloreada a la derecha |
| Galería de capturas | "No es un mockup": 2 capturas reales del dashboard nativo de Ghost (Ghost 6.57) en producción, cards glass con marco browser (3 dots + dominio), lazy loading y lightbox accesible |
| Stats counters | 6 contadores con count-up al entrar en viewport (IntersectionObserver): 13 pipes, 2 deps Go, ~11 MB, <10 MB RAM, 0 salientes, 100% tests |
| Statement tipográfico grande | "El panel de analytics que ya tienes, alimentado por tu servidor." con gradiente en "ya tienes" |
| Expertise 3 columnas | Tres compromisos: AGPL para siempre / Community driven / Los datos no salen |
| Industries grid | Grid "para quién": publishers, comunidades, agencias, puristas de privacidad |
| FAQ acordeón | 5 preguntas reales del README, acordeón animado con grid-rows y aria-expanded |
| CTA final "Discover the future" | "Instala GhostBird hoy" con one-liner curl y build desde fuente, botones copiar |
| Marquee de texto final | Frases separadas por · en scroll infinito (Self-hosted · AGPL-3.0 · ...) |
| Footer | Logo + club + Ko-fi + disclaimer no-afiliación + copyright |

**Slots del propietario**: `data-slot="field-notes-1"` (tras stats) y
`data-slot="field-notes-2"` (tras FAQ), como cards glass con borde discontinuo.

## Sistema (Phase 1)

- **Tipografía**: Sora (display: headlines, números, statement) + Inter (body).
  Máximo 2 familias de Google Fonts. Código con pila mono de sistema
  (ui-monospace/SF Mono/Menlo), sin tercera familia web.
- **Color** (AA verificado por script en ambos temas, ver §Auditoría):
  - **Dark (por defecto)**: canvas `#0B1020` (noche índigo) con glow/viñeta
    fija (radiales cian/violeta muy tenues), surface glass `#121830`,
    ink `#E9EDF7`, muted `#9AA5BF`, accent cian espectral `#7FDBCA`,
    accent-ink `#0B1020`, gradiente de TEXTO `#7FDBCA → #A78BFA` (violeta).
  - **Light**: canvas `#F1F4FB`, surface `#FFFFFF`, ink `#182035`,
    muted `#4C5568`, accent `#0C6E60` (el cian se oscurece para sostener AA),
    gradiente de texto `#0C6E60 → #6D28D9`.
  - Reparto: indigo/tinta dominantes; el cian solo en acentos, iconos, números
    y botón primario. El violeta NUNCA aparece salvo como extremo del
    gradiente de texto (headline y statement).
- **Glass**: `backdrop-filter: blur(14px)` + borde 1px translúcido + fondo
  rgba al 4% (dark) o 66% blanco (light). Fallback sólido vía `@supports`.
- **Radios**: 16px cards (12px chips internas), 999px botones y pills.
- **Sombras**: una sola sombra difusa suave por card elevada (mockup, nav,
  install); nada de sombras apiladas.
- **Signature move**: el pájaro fantasma. Logo en nav/footer, marca de agua
  gigante flotando en el hero (fill al 5% del cian), favicon con la nueva paleta.
- **Iconografía**: sprite SVG local de Lucide (24px, stroke 1.75), sin CDN ni
  dependencias JS externas.
- **Motion**: ver §Motion (sistema unificado de entrada). Marquee infinito
  (38s lineal), levitación del pájaro (9s), pulse-dot del eyebrow, count-up con
  ease-out cúbico (1.1s), acordeón con grid-rows (280ms). Todo congelado con
  `prefers-reduced-motion`; el marquee pasa a scroll manual.

## Motion (Phase 3: sistema unificado de entrada)

Personalidad **Premium** para las entradas; **Corporate** para el
micro-feedback. Un solo sistema (`[data-reveal]` + `.rv`/`.in`/`.done` en
`app.js`/`styles.css`), sin convivencia con el reveal del datasheet.

**Variables CSS** (en `:root`, fuente de verdad):

| Variable | Valor | Uso |
|---|---|---|
| `--dur-enter` | `420ms` | Duración de entrada (Premium 350-600ms) |
| `--ease-enter` | `cubic-bezier(0.4, 0, 0.2, 1)` | Curva de entrada (decelerate, 0% overshoot) |
| `--stagger` | `80ms` | Separación entre hermanos de un grupo |
| `--enter-y` | `22px` | Distancia de entrada (soft: 8px) |
| `--dur-feedback` | `90ms` | Respuesta de hover (Corporate, < 100ms) |

Entrada = position + opacity (nunca solo opacity), dirección siempre desde
abajo. El stagger es `calc(var(--i) * --stagger)` con `--i` asignado por JS
según el orden dentro del `[data-reveal-group]`.

**Grupos y tiempos** (presupuesto de stagger = último delay, siempre < 500ms):

| Grupo | Items | Stagger | Último delay | Notas |
|---|---|---|---|---|
| Hero (al load) | 5 | 60ms | 240ms | Override local `--dur-enter: 350ms`; eyebrow → headline → sub → CTAs → mockup; total < 600ms |
| Features 2x2 | 4 | 80ms | 240ms | translateY 22px + opacity |
| Split cómo funciona | 2 | 80ms | 80ms | Copía y card de código |
| Galería | 2 | 80ms | 80ms | Cards glass con marco browser |
| Stats | 6 | 80ms | 400ms | El count-up mantiene su propio IO (threshold 0.35) y manda el momento |
| Statement | 1 | - | 0ms | Soft (8px) |
| Tres compromisos | 3 | 80ms | 160ms | Columnas |
| Para quién | 4 | 80ms | 240ms | Dirección consistente (desde abajo) |
| FAQ | 5 | 60ms | 240ms | Soft (opacity + 8px) |
| Slots / install | 1 c/u | - | 0ms | Cards sueltas |

**Capas de motion** (skill motion-design):
- Primaria: la entrada de las cards.
- Secundaria: hover Premium en cards glass (`.rv.done:hover`: lift 3px + borde
  acento, 90ms). El perfil de hover solo se activa cuando la entrada termina
  (`.done` vía `transitionend`), para no pisar la transición de entrada.
- Ambiente: respiración del glow tras el mockup del hero
  (`gb-breathe`, 7s ease-in-out, opacidad 0.45 → 0.9). Nada más.

**Sin animar**: chips del marquee (el marquee ya vive), nav, footer, section
heads. Se descartó la sombra "que llega después" en las feature cards: las
glass no llevan sombra en reposo (regla de una sola sombra por card elevada) y
animarla pagaría un repaint por el que no aportaba.

**Reduced motion**: todo visible sin transición (`.rv` estático + kill global
de duraciones), lightbox instantáneo, sin cuenta atrás en el count-up.

**Lightbox** (Corporate): overlay `rgba(4,7,18,.86)`, cierre con Escape, clic
fuera y botón; entrada 220/260ms decelerate, salida 150ms accelerate
(`[hidden]` de vuelta al terminar). `role="dialog"` + `aria-modal`, foco al
botón de cerrar y devolución al disparador.

## Craft layer (Phase 2)

- **Layout**: contenedor 1120px. Hero 2 columnas (copy + mockup) desde 980px.
  Stats 3x2 desde 900px. "Para quién" 4 columnas desde 1020px. FAQ e install
  centradas a 780px. Móvil 390px: todo a 1 columna, nav-links ocultos.
- **Componentes**: botones pill (primario cian relleno, secundario ghost),
  chips de marquee, cards glass con hover lift (translateY 3px + borde cian),
  acordeón accesible (button + aria-expanded + region), bloques de código
  SIEMPRE oscuros (tokens propios, contraste verificado aparte).
- **Accesibilidad**: AA por script (9 pares por tema + 3 de código); skip-link;
  targets ≥36px; aria-labels i18n; mockup decorativo con aria-hidden; 1 h1.
- **SEO**: title/description ES/EN, OG/Twitter con og.png real (1200x630),
  canonical + hreflang es/en/x-default, JSON-LD SoftwareApplication +
  Organization + 2 ImageObject (capturas) + FAQPage, robots.txt, sitemap.xml.
- **i18n**: diccionario único `i18n.js` (ES/EN paridad verificada por script),
  auto por navegador + `?hl=` + localStorage `ghostbird-lang`; selector ES/EN.
- **Temas**: dark por defecto (Luminex es dark-first); toggle sol/luna con
  persistencia `ghostbird-theme` y meta theme-color dinámica.

## Auditoría anti-slop (resultado)

- Gradientes SOLO en texto (headline + statement + sparkline del mockup
  decorativo), nunca en botones, banners ni fondos de card. PASS.
- Sin Inter/Roboto como identidad: Inter es body, Sora es display. PASS.
- Sin lorem, sin claims sin fuente (13 pipes, 2 deps, ~11 MB, <10 MB RAM,
  0 salientes y 100% tests salen del repo/README verificados). PASS.
- Cero em/en dashes en prosa (grep verificado). PASS.
- Marquee con contenido real (facts del protocolo), no logos falsos de
  clientes. PASS.

## Changelog

- 2026-08-16: creación del datasheet industrial (rama feat/landing).
- 2026-08-17: rediseño completo a tema Luminex adaptado por decisión del
  propietario (rama feat/landing-luminex). Copy reutilizado y reorganizado
  a claves nuevas; SEO, favicon-pájaro, slots y verificación conservados.
- 2026-08-17: sistema unificado de entrada Premium con stagger y hover
  Corporate (rama feat/landing-motion) + galería "No es un mockup" con
  capturas reales del dashboard y lightbox accesible. og.png sin cambios
  (el hero no cambia); screenshot.png regenerado.
