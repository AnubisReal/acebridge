/* Workspace chrome and overview. Never starts or stops playback just to show status. */
(() => {
  document.querySelector('.skip-link').addEventListener('click', event => {
    event.preventDefault();
    const main = document.querySelector('#main-content');
    main.focus();
    main.scrollIntoView({block:'start'});
  });
  const themeKey = 'acebridge-theme';
  function applyTheme(theme) {
    document.documentElement.dataset.theme = theme;
    document.querySelector('meta[name="theme-color"]').content = theme === 'dark' ? '#1d1d1b' : '#e9e9e7';
    const mobileToggle = document.querySelector('#mobile-theme-toggle');
    mobileToggle.textContent = theme === 'dark' ? '☼' : '☾';
    mobileToggle.setAttribute('aria-label', theme === 'dark' ? 'Activar tema claro' : 'Activar tema oscuro');
    document.querySelectorAll('[data-theme]').forEach(button => {
      if (button.tagName === 'BUTTON') button.setAttribute('aria-pressed', String(button.dataset.theme === theme));
    });
  }
  let savedTheme = 'light';
  try { savedTheme = localStorage.getItem(themeKey) === 'dark' ? 'dark' : 'light'; } catch { /* Storage can be disabled. */ }
  applyTheme(savedTheme);
  document.querySelectorAll('button[data-theme]').forEach(button => button.addEventListener('click', () => {
    applyTheme(button.dataset.theme);
    try { localStorage.setItem(themeKey, button.dataset.theme); } catch { /* Theme still works without persistence. */ }
  }));
  document.querySelector('#mobile-theme-toggle').addEventListener('click', () => {
    const theme = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark';
    applyTheme(theme);
    try { localStorage.setItem(themeKey, theme); } catch { /* Optional persistence. */ }
  });

  function syncNavigation() {
    const view = document.querySelector('.view.active')?.id.replace('view-', '') || 'overview';
    document.querySelectorAll('#navigation [data-view], .workspace-rail [data-view]').forEach(button => {
      const selected = button.dataset.view === view;
      button.classList.toggle('active', selected);
      if (selected) button.setAttribute('aria-current', 'page');
      else button.removeAttribute('aria-current');
    });
    if (view === 'overview') {
      document.querySelector('#page-title').textContent = 'Tu televisión, organizada.';
      document.querySelector('#page-subtitle').textContent = 'Canales, fuentes y señales en directo desde un solo lugar.';
    }
    document.title = `${views[view]?.[0] || 'Resumen'} · AceBridge`;
  }
  window.addEventListener('hashchange', syncNavigation);
  document.addEventListener('acebridge:viewchange', syncNavigation);
  syncNavigation();

  function decorateChannels() {
    document.querySelectorAll('#quick-channel-list .quick-channel').forEach(button => {
      const channel = state.quickChannels.find(item => String(item.id) === button.dataset.playChannel);
      if (!channel) return;
      let footer = button.querySelector('.quick-source');
      if (!footer) {
        footer = document.createElement('span');
        footer.className = 'quick-source';
        footer.append(document.createElement('span'), document.createElement('em'));
        button.append(footer);
      }
      const source = state.sources.find(item => item.id === channel.source_id);
      const sourceName = source?.name || channel.source_name || 'Fuente';
      const connection = channel.stream_type === 'acestream' ? 'AceStream' : source?.proxy_url ? 'Túnel' : 'Directa';
      if (footer.firstChild.textContent !== sourceName) footer.firstChild.textContent = sourceName;
      if (footer.lastChild.textContent !== connection) footer.lastChild.textContent = connection;
    });
  }
  const catalogObserver = new MutationObserver(decorateChannels);
  catalogObserver.observe(document.querySelector('#quick-channel-list'), {childList:true});
  catalogObserver.observe(document.querySelector('#source-list'), {childList:true});
  decorateChannels();

  let refreshing = false;
  async function refreshOverview() {
    if (refreshing || document.hidden || !document.querySelector('#view-overview').classList.contains('active')) return;
    refreshing = true;
    try {
      const [streamsResult, epgResult] = await Promise.allSettled([request(`${api}/streams`), request(`${api}/epg`)]);
      if(streamsResult.status==='fulfilled'){
      const streams=streamsResult.value;
      document.querySelector('#overview-stream-count').textContent = streams.length;
      const list = document.querySelector('#overview-stream-list');
      list.replaceChildren();
      if (!streams.length) {
        const note = document.createElement('p');
        note.className = 'inspector-note';
        note.textContent = 'No hay emisiones activas.';
        list.append(note);
      }
      streams.slice(0, 4).forEach(stream => {
        const row = document.createElement('article');
        row.className = 'overview-stream';
        const mark = document.createElement('span');
        mark.setAttribute('aria-hidden', 'true');
        mark.textContent = (stream.channel_name || 'C').slice(0, 2).toUpperCase();
        const copy = document.createElement('div');
        const name = document.createElement('strong');
        name.textContent = stream.channel_name || `Canal ${stream.channel_id}`;
        const detail = document.createElement('small');
        detail.textContent = `${formatDuration(stream.duration_seconds)} · ${Number(stream.viewers) || 0} espectadores`;
        copy.append(name, detail);
        row.append(mark, copy);
        list.append(row);
      });
      if (streams.length > 4) {
        const note = document.createElement('p');
        note.className = 'inspector-note';
        note.textContent = `Y ${streams.length - 4} emisiones más en el monitor.`;
        list.append(note);
      }
      }else{
        document.querySelector('#overview-stream-count').textContent='—';
        document.querySelector('#overview-stream-list').replaceChildren(Object.assign(document.createElement('p'),{className:'inspector-note',textContent:'No se pudo consultar la actividad.'}));
      }
      if(epgResult.status==='fulfilled'){
      const epg=epgResult.value;
      document.querySelector('#overview-epg-status').textContent = !epg.enabled ? 'Guía desactivada. Puedes configurar una EPG independiente.' : epg.last_error ? `Requiere atención: ${epg.last_error}` : epg.cached_at ? `Guía disponible · ${relativeTime(epg.cached_at)}` : 'Configurada, pendiente de descargar la guía.';
      }else{
        document.querySelector('#overview-epg-status').textContent='No se pudo consultar el estado de la guía.';
      }
      const base = (state.settings?.public_base_url || location.origin).replace(/\/$/, '');
      document.querySelector('#overview-playlist-url').value = `${base}/playlist.m3u`;
    } catch {
      document.querySelector('#overview-stream-count').textContent = '—';
      document.querySelector('#overview-stream-list').replaceChildren(Object.assign(document.createElement('p'), {className:'inspector-note', textContent:'No se pudo consultar la actividad. Usa Actualizar para reintentarlo.'}));
      document.querySelector('#overview-epg-status').textContent = 'No se pudo consultar el estado de la guía.';
    } finally { refreshing = false; }
  }
  document.querySelector('#overview-copy-playlist').addEventListener('click', async () => {
    const input = document.querySelector('#overview-playlist-url');
    if (!input.value) { toast('La URL todavía no está disponible', true); return; }
    try { await navigator.clipboard.writeText(input.value); toast('URL de la lista copiada'); }
    catch { input.focus(); input.select(); toast('Selecciona y copia la URL manualmente', true); }
  });
  document.querySelector('#refresh-button').addEventListener('click', refreshOverview);
  window.addEventListener('hashchange', refreshOverview);
  document.addEventListener('acebridge:settingsloaded', () => {
    const base = (state.settings?.public_base_url || location.origin).replace(/\/$/, '');
    document.querySelector('#overview-playlist-url').value = `${base}/playlist.m3u`;
  });
  document.addEventListener('visibilitychange', refreshOverview);
  setInterval(refreshOverview, 15000);
  refreshOverview();
})();
