# Rediseño de la interfaz · 19 de octubre de 2026

## Punto de retorno

Etiqueta **local**: `restore/pre-tgstrm-ui-2026-10-19`.
Commit de partida: `057cb91f11209a9cba566a0041d5f6b0bfb6f6bf`.
El árbol estaba limpio al crear la etiqueta. No se ha publicado ni desplegado este rediseño.
La etiqueta permite recuperar la versión anterior sin depender de los archivos nuevos.
No incluye copias de bases de datos: esta intervención no migra ni modifica datos de producción.

## Alcance

- Estética inspirada en la estructura de TgStrm: fondo gris cálido, navegación superior flotante, barra estrecha de iconos y área de trabajo redondeada.
- Temas claro y oscuro, con preferencia local opcional y control accesible también en móvil.
- Catálogo, fuentes, monitor, salida M3U, EPG independiente, configuración y diálogos conservan sus controladores.
- Resumen con actividad y estado EPG reales. Sus consultas son de lectura y no inician emisiones.
- En móvil, la navegación pasa a una barra inferior y los formularios permiten desplazamiento vertical.
- En la primera fase visual no se cambiaron el proxy, el túnel, el reproductor ni las reglas de sesiones/concurrencia. Las correcciones posteriores se detallan abajo.
- Se mantiene la fuente de sistema; no se han copiado fuentes tipográficas ni dependencias de TgStrm.

## Verificación

Instancia de pruebas en localhost; datos ficticios en un directorio temporal fuera del repositorio.

- Navegador Chromium: las seis vistas en 320, 390, 768, 1024, 1280 y 1440 px (36 combinaciones), sin desbordamientos horizontales detectados.
- Catálogo vacío y con 68 entradas; nombres largos, comillas y caracteres HTML tratados como texto.
- Paginación: 50 entradas en la primera página y 18 en la segunda; búsqueda; selección y cancelación de selección múltiple.
- Formulario de fuentes, cambio a canal manual, activación de los campos de túnel y edición de canal en 320 px.
- Temas, navegación y comprobación de consola.
- Recursos web embebidos, unicidad de IDs, destinos de navegación y presencia de los controles existentes cubiertos por tests de Go.
- Regresión de escape HTML en texto y atributos, recursos referenciados y sintaxis JavaScript cubiertos sin dependencias externas.

Comandos reproducibles:

```sh
go test -race ./...
go vet ./...
node --test scripts/workspace.test.cjs
git diff --check
```

## Correcciones encontradas durante la revisión

- Una altura heredada de la navegación móvil ocupaba toda la pantalla; se restableció su altura automática.
- Campos de búsqueda sin nombre accesible; se añadieron etiquetas ARIA.
- Comillas de los nombres del proveedor no escapadas al interpolar atributos; se corrigió y añadió regresión.
- Contraste de acciones y texto secundario en el tema oscuro.
- Enlace de salto al contenido sin alterar la vista activa.

## Límites de la auditoría

Las pruebas móviles son de viewport en Chromium, no de un dispositivo físico ni de Safari iOS.
Los enlaces de los canales de muestra son ficticios: esta revisión valida la interfaz, no certifica la reproducción de cada proveedor.
Un cambio visual por sí solo no corrige el límite de emisiones; véase la corrección posterior de liberación de sesiones.

## Comprobación posterior con una señal real

Se corrigió el foco del buscador: el contorno se dibuja en la cápsula, sin rectángulo interior, conservando una indicación accesible de foco.
La instancia visual inicial usaba enlaces `example.com` deliberadamente ficticios, no reproducibles.
Se conservaron esos datos y se arrancó el mismo puerto con una copia independiente del catálogo TDT existente, en un directorio temporal separado, con su proxy WARP.
La 1 se verificó en el reproductor web: estado «En directo», vídeo 1280×720, `readyState=4`, tiempo de reproducción avanzando y sin errores de consola.
Esto comprueba esa señal, no garantiza la disponibilidad de todos los canales de la lista.

## Correcciones posteriores a la auditoría funcional

- Escape, cierre nativo y salida de página limpian el vídeo, la instancia HLS y el heartbeat. Las respuestas de preparación que lleguen tras el cierre liberan su espectador y no arrancan vídeo.
- El cierre del último espectador conocido libera inmediatamente el canal. Un espectador desconocido no lo elimina y cerrar uno no detiene a los demás. El apagado del motor se serializa con los arranques del mismo canal.
- Se conserva el límite global configurado. Los reproductores externos que no notifican su cierre siguen dependiendo del tiempo de inactividad: no se eliminan sesiones potencialmente activas por adivinación.
- Los campos de modos de importación inactivos están deshabilitados y los obligatorios se ajustan a la pestaña.
- Los filtros cancelan la petición anterior y descartan respuestas fuera de orden, incluidos sus totales. Los errores visibles no quedan como promesas sin capturar.
- El navegador no intenta usar una URL privada de Ace Engine como alternativa si falla la preparación.
- Contraste móvil oscuro de Fuentes corregido; nombre accesible de la URL M3U y texto del pie del reproductor corregidos.
- Los botones de copiar de M3U/EPG muestran un error y seleccionan el campo si falla el portapapeles.
- El estado de EPG y el de actividad se resuelven de manera independiente.

Regresiones: once pruebas Node (incluidas respuestas fuera de orden, cierre durante carga, Escape, campos ocultos, fallo de preparación y fallo de EPG), pruebas Go de último espectador y diez cambios rápidos IPTV con límite uno, además de la suite existente con `-race` y `go vet`.

Verificación en navegador: La 1 reproduce a 1280×720; Escape deja el vídeo pausado, sin fuente y el endpoint de sesiones devuelve `[]`. Contraste oscuro verificado a 320 px. No se modifican datos de producción ni se publica una imagen Docker como parte de esta revisión.
