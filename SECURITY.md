# Seguridad

AceBridge no incorpora autenticación y está diseñado principalmente para una red privada. Si se publica la interfaz, cualquier visitante con acceso podrá administrar fuentes, canales y configuración.

## Informar de una vulnerabilidad

No publiques detalles explotables en una incidencia pública. Utiliza **Security → Report a vulnerability** en GitHub para enviar un aviso privado al mantenedor.

Incluye la versión afectada, pasos de reproducción, impacto estimado y, si existe, una propuesta de corrección. Las versiones mantenidas son la última etiqueta estable y `main`.

## Despliegue recomendado

- Mantén Ace Engine en una red privada.
- Expón AceBridge únicamente mediante un proxy inverso controlado.
- Usa HTTPS cuando el acceso salga de la red local.
- No habilites `ACEBRIDGE_ALLOW_PRIVATE_SOURCES` salvo que necesites importar listas desde la LAN.
- Fija una etiqueta versionada de la imagen para despliegues que requieran actualizaciones controladas.
