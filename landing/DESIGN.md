# DESIGN.md — Landing GhostBird (ghostbird.cloudless.club)

Fuente de verdad visual de la landing. Si CSS y este fichero divergen, manda este fichero.

## Contexto (Phase 0)

- **Artefacto**: landing técnica de un producto self-hosted para audiencia técnica
  (personas que autoalojan Ghost). Acción principal: entender la pieza y ponerla en
  marcha; acciones secundarias: conocer el club y apoyar en Ko-fi.
- **Restricción del propietario**: NO repetir la estructura de las landings previas
  del ecosistema (hero con claim, 3 features, comparativa, screenshots, FAQ, CTA).
- **Posicionamiento**: técnico, honesto, sereno. Sin hype, con el humor seco del club.

## Dirección elegida: ficha técnica / datasheet industrial (opción c)

El producto ES literalmente una pieza de recambio ("drop-in replacement"). La web es
su ficha técnica: un documento industrial numerado con cajetín de plano, tablas de
especificaciones, notas de margen y un procedimiento de instalación. La historia del
hueco (Ghost 6 externalizó las estadísticas a un cloud propietario) va comprimida en
un bloque "nota de servicio" (§02), no como investigación larga.

Por qué no las otras: (a) guía de campo cansa el juego ornitológico más allá del
nombre; (b) el case study forense pide más narrativa de la que un self-hoster busca
al llegar. El datasheet es la única dirección donde el argumento central ("pieza de
sustitución fiel al contrato") ES la estructura.

**Adjetivos (commit)**: industrial, honesto, sereno, espectral, minucioso.
**Esencia**: "ficha técnica de un recambio que el fabricante no vende".

## Sistema (Phase 1)

- **Tipografía**: IBM Plex Sans (texto y títulos) + IBM Plex Mono (etiquetas, tablas,
  datos, código). Dos familias de Google Fonts, fallback de sistema. Escala ~1.25
  sobre base 16: 13/16/20/25/31/39 (título grande con clamp).
- **Color** (verificado AA por script, ver §Auditoría):
  - Claro: canvas `#f4f3ef` (papel técnico frío), sheet `#fbfaf7`, ink `#21242a`,
    muted `#5b5e66`, line `#d9d7cf`, accent `#b04a00` (naranja de señalización
    quemado), accent-ink `#ffffff`, ok `#2e6b3f`.
  - Oscuro: canvas `#14161a`, sheet `#1a1d22`, ink `#e8e6df`, muted `#a3a6ad`,
    line `#2d3037`, accent `#ff9c5b`, accent-ink `#14161a`, ok `#7fbf8e`.
  - Reparto ~60-30-10: papel/tinta dominantes; el naranja solo en números de
    sección, enlaces, cotas del plano y el botón principal.
- **Radios**: 0 px en todo el documento (piezas industriales rectas).
- **Sombras**: ninguna. Jerarquía con hairlines de 1 px; en oscuro, elevación por
  ligereza del sheet sobre el canvas.
- **Signature move**: el cajetín de plano técnico (title block) con el pájaro
  fantasma acotado ("1 binario", "1 fichero SQLite") y campos de plano (Nº PIEZA
  GB-1, MATERIAL, ESCALA, REV). Reaparece comprimido en el footer.
- **Adaptador**: CSS custom properties en `styles.css` con temas por
  `[data-theme="light"|"dark"]`.

## Craft layer (Phase 2)

- **Layout**: hoja enmarcada (`.sheet`) sobre el canvas, con franja de documento
  arriba (código + dominio). Secciones en grid de 3 columnas en escritorio:
  número de sección (§01) / contenido / notas de margen. En móvil colapsa a una
  columna y las notas de margen pasan a cajas insertadas. Márgenes generosos entre
  secciones; el documento no es una columna centrada reflexiva.
- **Componentes**: botones rectangulares mono en mayúsculas (primario relleno
  naranja, secundario hairline); tablas con hairlines y monospace; bloque de
  código con borde; pasos numerados de procedimiento. Estados hover/active/focus
  definidos; focus visible de 2 px.
- **Motion**: revelado sutil al scroll (translateY 8 px, 180 ms ease-out) y UN solo
  guiño: el pájaro del cajetín levita (translateY ±3 px, 8 s). Todo congelado con
  `prefers-reduced-motion`.
- **Iconografía**: Lucide inline (sun, moon, languages, external-link). Stroke
  1.75, 18 px. Sin emoji.
- **Imagery**: sin stock ni capturas falsas. El único gráfico es el plano del
  pájaro fantasma, dibujado con cotas técnicas. La imagen OG es la propia ficha.
- **Dark mode**: diseñado, no invertido (papel frío → grafito azulado; naranja se
  aclara y desatura un punto).
- **Accesibilidad**: AA calculado por script en ambos temas; skip-link; targets
  ≥24 px; aria-labels i18n; html lang dinámico.

## i18n y temas (convención del ecosistema)

- Un solo `index.html` con diccionario `i18n.js` (patrón cloudless.club): ES
  por defecto, EN con `?hl=en`, auto por navegador, persistencia en
  localStorage (`ghostbird-lang`), switcher ES/EN.
- Tema claro/oscuro con toggle sol/luna + seguimiento de `prefers-color-scheme`
  si no hay preferencia guardada (`ghostbird-theme`). Sin scripts inline
  (CSP-friendly, como el club).

## Auditoría anti-slop (resultado)

- Sin hero gradiente ni tarjetas de 3 features ni FAQ ni carrusel de capturas:
  la estructura es un documento numerado. PASS.
- Sin Inter/Roboto: IBM Plex. Sin indigo/violeta: papel + tinta + naranja. PASS.
- Sin radios blandos ni sombras difusas apiladas. PASS.
- Contraste AA verificado por script (12 pares: ink/muted/accent/accent-ink × 2
  temas). PASS (evidencia en el informe de la rama).
- Cero em/en dashes en prosa; sin "seamless/robust"; grep verificado. PASS.

## Changelog

- 2026-08-16: creación junto con la landing (rama feat/landing).
