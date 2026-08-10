<p align="center">
  <img src="internal/app/web/brand/acebridge-logo.png" alt="AceBridge" width="360">
</p>

# AceBridge

AceBridge administra listas M3U, TXT y catálogos JSON con canales AceStream, detecta emisiones de Premier Padel y genera una salida HLS estable para Jellyfin. La reproducción integrada y la playlist utilizan el proxy de AceBridge, por lo que Jellyfin no necesita conectarse directamente a Ace Engine.

AceBridge está diseñado como servicio local monousuario. No incluye cuentas, contraseñas ni roles.

## Inicio rápido con Docker Compose

```bash
cp .env.example .env
docker compose up -d
```

La interfaz estará disponible en `http://localhost:5023` y la playlist en `http://localhost:5023/playlist.m3u`.

El compose local conserva la base de datos en el volumen persistente `acebridge_data`. Puedes cambiar su nombre con `ACEBRIDGE_VOLUME`. Para actualizar:

```bash
docker compose pull
docker compose up -d
```

## Instalación sencilla en Linux sin Docker

Solo necesitas `git`. Copia y pega estos tres comandos:

```bash
git clone https://github.com/anubisreal/acebridge.git
cd acebridge
sudo ./install.sh
```

El instalador descarga la versión correcta para tu servidor, comprueba que sea auténtica, instala el servicio y lo arranca. No necesitas instalar Go, crear usuarios, copiar archivos ni configurar carpetas.

Cuando aparezca el mensaje de instalación terminada, abre:

```text
http://IP_DEL_SERVIDOR:8080
```

Para administrar AceBridge utiliza el menú:

```bash
sudo ./install.sh menu
```

El menú permite instalar o actualizar, cambiar el puerto, comprobar el estado, ver los logs y desinstalar. Para actualizar también puedes volver a ejecutar `sudo ./install.sh`.

La configuración y los canales se conservan automáticamente en `/var/lib/acebridge`, incluso al desinstalar.

## Stack con la imagen de GitHub

El archivo `stack.yaml` está preparado para Portainer y otros gestores compatibles con Compose. Utiliza directamente:

```text
ghcr.io/anubisreal/acebridge:latest
```

El stack usa el volumen persistente `acebridge_data`. Estas variables son opcionales:

| Variable | Predeterminado | Uso |
| --- | --- | --- |
| `ACEBRIDGE_IMAGE` | `ghcr.io/anubisreal/acebridge:latest` | Imagen o versión que se desplegará |
| `ACEBRIDGE_PORT` | `5023` | Puerto publicado en el servidor |
| `ACEBRIDGE_VOLUME` | `acebridge_data` | Nombre del volumen persistente |
| `TZ` | `Europe/Madrid` | Zona horaria del contenedor |
| `ACEBRIDGE_ALLOW_PRIVATE_SOURCES` | `false` | Permite importar fuentes desde redes privadas |

Para despliegues controlados es preferible fijar una versión, por ejemplo `ghcr.io/anubisreal/acebridge:1.0.0`, en lugar de `latest`.

## Publicación en GitHub y GHCR

1. Crea en GitHub un repositorio llamado `acebridge` bajo la cuenta `anubisreal`.
2. Sube el proyecto a la rama `main`.
3. GitHub Actions ejecutará formato, `go vet`, pruebas con detector de carreras, cobertura, Staticcheck, Govulncheck, arranque del contenedor y Trivy.
4. Si todo termina correctamente, se publicarán imágenes `linux/amd64` y `linux/arm64` en `ghcr.io/anubisreal/acebridge`.
5. En la primera publicación abre **Packages → acebridge → Package settings** y cambia la visibilidad a pública si deseas que el stack descargue la imagen sin credenciales.

Los pushes a `main` publican `latest` y una etiqueta `sha-*`. Para crear una versión estable:

```bash
git tag v1.0.0
git push origin v1.0.0
```

La etiqueta `v1.0.0` publica `1.0.0`, `1.0`, `1` y `sha-*`. Las imágenes incluyen procedencia, SBOM y firma keyless.

## Jellyfin

En Jellyfin abre **Panel de control → TV en directo → Proveedores de sintonización → M3U Tuner**.

Si Jellyfin comparte la red Docker utiliza:

```text
http://acebridge:8080/playlist.m3u
```

Si está en otra máquina, utiliza la dirección local o pública de AceBridge. Configura primero la URL pública desde la interfaz para que las entradas de la playlist tengan el dominio correcto.

## Ace Engine

Configura desde AceBridge la dirección de Ace Engine, normalmente `http://IP_DEL_SERVIDOR:6878`. Ace Engine puede permanecer dentro de la red local aunque AceBridge se publique mediante HTTPS.

AceBridge comparte una sesión por canal, controla espectadores, libera la sesión después del tiempo de inactividad configurado y utiliza `command_url` cuando Ace Engine lo proporciona.

## Proxy inverso y HTTPS

En Nginx Proxy Manager crea un Proxy Host hacia el puerto publicado de AceBridge, activa HTTPS y guarda en AceBridge la URL pública completa. No es necesario publicar el puerto de Ace Engine.

AceBridge no confía automáticamente en `X-Forwarded-*`; la URL pública configurada es la referencia canónica.

> AceBridge no tiene autenticación. Si publicas la interfaz, cualquier visitante con acceso podrá administrar canales, fuentes y configuración. Restringe el acceso desde Nginx Proxy Manager, una VPN o la red cuando sea necesario.

## Fuentes remotas y red local

Por defecto se bloquean loopback, link-local, direcciones de metadatos cloud y redes privadas para las fuentes descargadas. Si una lista M3U o JSON está alojada deliberadamente en tu LAN, establece:

```env
ACEBRIDGE_ALLOW_PRIVATE_SOURCES=true
```

Esta excepción no es necesaria para Ace Engine.

## Desarrollo local

Requisitos: Go 1.26 o posterior.

```bash
go run ./cmd/acebridge
```

Variables del proceso:

| Variable | Predeterminado | Uso |
| --- | --- | --- |
| `ACEBRIDGE_LISTEN_ADDR` | `:8080` | Dirección HTTP de escucha |
| `ACEBRIDGE_DATA_DIR` | `./data` | Directorio persistente de SQLite |
| `ACEBRIDGE_ALLOW_PRIVATE_SOURCES` | `false` | Permite descargar fuentes alojadas en redes privadas |
| `ACEBRIDGE_IPTV_ORG_API` | `https://iptv-org.github.io/api` | Catálogo de nombres y logos |
| `ACEBRIDGE_HEALTHCHECK_URL` | `http://127.0.0.1:8080/health/live` | URL usada por la comprobación interna del contenedor |

Comprobaciones locales:

```bash
make check
make image
```

El binario también admite:

```bash
acebridge version
acebridge healthcheck
```

## Operación

- Vida del proceso: `/health/live`
- Preparación: `/health/ready`
- Métricas Prometheus: `/metrics`
- Playlist Jellyfin: `/playlist.m3u`
- API: `/api/v1`

El contenedor se ejecuta como usuario no privilegiado, sin capacidades Linux adicionales, con el sistema de archivos de solo lectura y una comprobación de salud nativa. Los datos persistentes se escriben únicamente en `/data`.

## Seguridad

Consulta [SECURITY.md](SECURITY.md) antes de exponer el servicio públicamente.

## Licencia

MIT
