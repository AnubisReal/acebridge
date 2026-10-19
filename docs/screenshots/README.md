# Capturas públicas anonimizadas

- `overview-desktop-private.png`: vista de escritorio.
- `overview-mobile-private.png`: vista móvil.

Son imágenes de documentación, no recursos que se cargan en la aplicación. Se excluyen del contexto Docker. Los originales permanecen fuera del repositorio.

Se utilizó la herramienta integrada de edición de imágenes (no CLI) sobre capturas reales. Se inspeccionaron los resultados antes de su publicación.

Instrucciones de edición utilizadas:

- Escritorio: conservar distribución, controles, tipografía y marca AceBridge; desenfocar con intensidad todos los logos y nombres de canales, nombres de fuentes y consulta del buscador; reemplazar las cuatro cifras de biblioteca por guiones y la URL de salida por `https://tv.example.com/playlist.m3u`; no añadir secciones ni inventar estados operativos.
- Móvil: conservar distribución y proporción móvil; desenfocar logos completos y parcialmente visibles, nombres de canales y fuentes y consulta del buscador; ocultar cifras reales; mantener marca y controles; no publicar direcciones, credenciales ni identidades privadas.

Para futuras capturas: no incluir cuentas, URLs con tokens, direcciones reales de servidores, rutas personales, credenciales, formularios de configuración o contenido de reproducción. No basta con un desenfoque ligero para anonimizar secretos: deben eliminarse o sustituirse por datos ficticios.
