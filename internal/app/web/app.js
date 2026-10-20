const api='/api/v1';
const state={channels:[],quickChannels:[],quickTotal:0,quickLoading:false,quickRequest:0,sources:[],settings:null,sourceMode:'url',playerHLS:null,playerToken:0,playerSession:null,playerHeartbeat:null,quickMode:'all',quickQuery:'',quickLimit:60,quickRecent:[],monitor:{streams:[],jobs:[],history:[]},monitorLoading:false};
const views={overview:['Resumen','Estado general de tu televisión en directo'],channels:['Canales','Gestiona qué canales aparecen en la salida'],sources:['Fuentes','Importa y sincroniza listas M3U y JSON'],monitor:['Monitor','Actividad, señal y salud de AceBridge'],output:['Salida Jellyfin','Conecta AceBridge con tu servidor multimedia'],settings:['Configuración','Conexión, acceso y automatización']};
const $=selector=>document.querySelector(selector);
const $$=selector=>[...document.querySelectorAll(selector)];
$('#source-file').accept='.m3u,.m3u8,.txt,.json,text/plain,audio/x-mpegurl,application/json';
$('#source-file-fields label').textContent='Archivo M3U, TXT o JSON';
try{const recent=JSON.parse(localStorage.getItem('acebridge-recent-channels')||'[]');state.quickRecent=Array.isArray(recent)?recent.map(Number).filter(Number.isFinite):[]}catch{state.quickRecent=[]}

async function request(path,options={}){const response=await fetch(path,options);if(response.status===204)return null;const data=await response.json().catch(()=>({}));if(!response.ok)throw new Error(data.error||`Error ${response.status}`);return data}
function toast(message,error=false){const el=$('#toast');el.textContent=message;el.className=error?'visible error':'visible';clearTimeout(toast.timer);toast.timer=setTimeout(()=>el.className='',3200)}
function escapeHTML(value=''){const el=document.createElement('div');el.textContent=value;return el.innerHTML.replace(/"/g,'&quot;').replace(/'/g,'&#39;')}
async function copyField(selector,message='URL copiada'){
  const input=$(selector);
  try{await navigator.clipboard.writeText(input.value);toast(message)}
  catch{input.focus();input.select();toast('No se pudo copiar. La URL está seleccionada para copiarla manualmente.',true)}
}
function closeMobileMenu(){$('.sidebar').classList.remove('open');$('#sidebar-backdrop').classList.remove('visible');document.body.classList.remove('menu-open');$('#menu-button').setAttribute('aria-expanded','false')}
function toggleMobileMenu(){const opening=!$('.sidebar').classList.contains('open');$('.sidebar').classList.toggle('open',opening);$('#sidebar-backdrop').classList.toggle('visible',opening);document.body.classList.toggle('menu-open',opening);$('#menu-button').setAttribute('aria-expanded',String(opening))}
function showView(name,updateHash=true){if(!views[name])name='overview';$$('.view').forEach(el=>el.classList.toggle('active',el.id===`view-${name}`));$$('.nav-item').forEach(el=>el.classList.toggle('active',el.dataset.view===name));$('#page-title').textContent=views[name][0];$('#page-subtitle').textContent=views[name][1];$('#refresh-button').classList.toggle('hidden',name==='channels');$('#add-channel').classList.toggle('hidden',name!=='channels');closeMobileMenu();if(updateHash&&location.hash!==`#${name}`)location.hash=name;if(name==='monitor')refreshMonitorLive();document.dispatchEvent(new CustomEvent('acebridge:viewchange',{detail:name}))}
async function loadHealth(){try{await request('/health');$('#health-dot').className='ok';$('#health-text').textContent='Sistema operativo';$('#database-status').textContent='Operativa'}catch{$('#health-dot').className='error';$('#health-text').textContent='Sin conexión';$('#database-status').textContent='Sin conexión'}}
async function loadDashboard(){const data=await request(`${api}/dashboard`);$('#stat-channels').textContent=data.channels;$('#stat-enabled').textContent=data.enabled_channels;$('#stat-sources').textContent=data.sources;$('#stat-groups').textContent=data.groups;const issues=[];if(data.failed_sources)issues.push(`${data.failed_sources} ${data.failed_sources===1?'fuente con error':'fuentes con errores'}`);if(data.problem_channels)issues.push(`${data.problem_channels} canales con problemas`);$('#incident-center').classList.toggle('hidden',issues.length===0);$('#incident-text').textContent=issues.join(' · ')}
function formatDuration(seconds=0){const value=Math.max(0,Number(seconds)||0);const hours=Math.floor(value/3600);const minutes=Math.floor((value%3600)/60);const secs=Math.floor(value%60);return hours?`${hours}h ${String(minutes).padStart(2,'0')}m`:`${minutes}m ${String(secs).padStart(2,'0')}s`}
function relativeTime(value){if(!value)return'—';const seconds=Math.max(0,Math.floor((Date.now()-new Date(value).getTime())/1000));if(seconds<60)return'Hace un momento';if(seconds<3600)return`Hace ${Math.floor(seconds/60)} min`;if(seconds<86400)return`Hace ${Math.floor(seconds/3600)} h`;return`Hace ${Math.floor(seconds/86400)} d`}
async function loadMonitor(){const [dashboard,streams,jobs,history]=await Promise.all([request(`${api}/dashboard`),request(`${api}/streams`),request(`${api}/sync-jobs`),request(`${api}/history`)]);state.monitor={streams,jobs,history};$('#monitor-stream-count').textContent=streams.length;$('#monitor-problem-count').textContent=dashboard.problem_channels||0;const latest=jobs[0];$('#monitor-sync-state').textContent=latest?latest.status==='completed'?'Correcta':latest.status==='error'?'Error':'En curso':'—';$('#monitor-sync-detail').textContent=latest?relativeTime(latest.finished_at||latest.started_at):'Sin actividad';try{const engine=await request(`${api}/engine/check`,{method:'POST'});$('#monitor-engine-state').textContent=engine.operational?'Operativo':'Error';$('#monitor-engine-detail').textContent=engine.operational&&engine.version?`Versión ${engine.version}`:engine.message||'No disponible'}catch{$('#monitor-engine-state').textContent='Sin conexión';$('#monitor-engine-detail').textContent='Revisa la configuración'}$('#monitor-stream-empty').classList.toggle('hidden',streams.length>0);$('#monitor-stream-list').classList.toggle('hidden',streams.length===0);$('#monitor-stream-list').innerHTML=streams.map(stream=>`<article class="monitor-row"><div class="monitor-row-main"><span class="monitor-channel-mark">${escapeHTML((stream.channel_name||'C').slice(0,1).toUpperCase())}</span><div><strong>${escapeHTML(stream.channel_name||`Canal ${stream.channel_id}`)}</strong><small class="monitor-live">En reproducción</small></div></div><div class="monitor-metric"><span>Duración</span><strong>${formatDuration(stream.duration_seconds)}</strong></div><div class="monitor-metric"><span>Espectadores</span><strong>${Math.max(0,Number(stream.viewers)||0)}</strong></div><div class="monitor-metric"><span>Última actividad</span><strong>${relativeTime(stream.last_access)}</strong></div></article>`).join('');const visibleJobs=jobs.slice(0,5);$('#monitor-job-empty').classList.toggle('hidden',visibleJobs.length>0);$('#monitor-job-list').classList.toggle('hidden',visibleJobs.length===0);$('#monitor-job-list').innerHTML=visibleJobs.map(job=>`<article class="monitor-row"><div class="monitor-row-main"><span class="monitor-channel-mark">${job.status==='completed'?'✓':job.status==='error'?'!':'↻'}</span><div><strong>Fuente ${job.source_id}</strong><small>${job.status==='completed'?`+${job.added} · ${job.updated} actualizados · -${job.removed}`:job.error||relativeTime(job.started_at)}</small></div></div><span class="monitor-row-status ${job.status==='completed'?'ok':job.status==='error'?'error':''}">${job.status==='completed'?'Completada':job.status==='error'?'Error':'En curso'}</span></article>`).join('');const visibleHistory=history.slice(0,5);$('#monitor-history-empty').classList.toggle('hidden',visibleHistory.length>0);$('#monitor-history-list').classList.toggle('hidden',visibleHistory.length===0);$('#monitor-history-list').innerHTML=visibleHistory.map(item=>`<article class="monitor-row"><div class="monitor-row-main"><span class="monitor-channel-mark">${escapeHTML((item.channel_name||'C').slice(0,1).toUpperCase())}</span><div><strong>${escapeHTML(item.channel_name||'Canal')}</strong><small>${relativeTime(item.started_at)}</small></div></div><span class="monitor-row-status ok">Preparada</span></article>`).join('')}
async function loadChannels(){state.channels=await request(`${api}/channels`);const select=$('#channel-group');const selected=select.value;const groups=[...new Set(state.channels.map(c=>c.group||'Sin grupo'))].sort((a,b)=>a.localeCompare(b));select.innerHTML='<option value="">Todos los grupos</option>'+groups.map(group=>`<option value="${escapeHTML(group)}">${escapeHTML(group)}</option>`).join('');select.value=groups.includes(selected)?selected:'';renderChannels()}
function broadcastLabel(channel,fallback){return channel.broadcast_state==='live'?'En directo':channel.broadcast_state==='replay'?'Repetición disponible':channel.broadcast_state==='unavailable'?'No disponible':fallback}
function sourceSyncRunning(id,jobs){return jobs.some(job=>Number(job.source_id)===Number(id)&&(job.status==='queued'||job.status==='running'))}
function sourceSyncLabel(source,jobs){return sourceSyncRunning(source.id,jobs)?'● Sincronizando…':source.last_status==='healthy'?'● Operativa':source.last_status==='error'?'● Error':source.last_status==='idle'?'● Sin emisiones disponibles':'● Pendiente'}
async function refreshSourceSync(){
  if(state.syncPolling||document.hidden)return;
  state.syncPolling=true;
  try{
    const jobs=await request(`${api}/sync-jobs`);
    const signature=JSON.stringify(jobs.map(job=>[job.id,job.status]));
    if(signature!==state.syncSignature){
      await Promise.all([loadSources(),loadChannels(),loadQuickChannels(),loadDashboard()]);
      state.syncSignature=signature;
    }
  }catch(error){console.error('Source synchronization refresh failed',error)}
  finally{state.syncPolling=false}
}
function renderChannels(){const term=$('#channel-search').value.trim().toLowerCase();const group=$('#channel-group').value;const channels=state.channels.filter(c=>(!term||c.name.toLowerCase().includes(term)||(c.group||'').toLowerCase().includes(term))&&(!group||(c.group||'Sin grupo')===group));$('#channel-count').textContent=`${channels.length} ${channels.length===1?'canal':'canales'}`;$('#channels-empty').classList.toggle('hidden',channels.length!==0);$('.channel-table-head').classList.toggle('hidden',channels.length===0);$('#channel-list').innerHTML=channels.map(c=>`<div class="channel-row"><button class="channel-identity" data-play-channel="${c.id}" title="Reproducir ${escapeHTML(c.name)}"><div class="channel-logo"><span>${escapeHTML(c.name.slice(0,1).toUpperCase())}</span>${c.logo?`<img src="${escapeHTML(c.logo)}" alt="" loading="lazy">`:''}</div><div class="channel-copy"><strong>${escapeHTML(c.name)}</strong><small><i></i>${escapeHTML(c.enabled?broadcastLabel(c,'Disponible'):'Oculto')} · ${escapeHTML(c.tvg_id||c.acestream_id.slice(0,10)+'…')}</small></div><span class="play-hint" aria-hidden="true"><img src="/icons/play.svg" alt=""></span></button><div class="channel-group"><span class="tag">${escapeHTML(c.group||'Sin grupo')}</span></div><div class="channel-source"><span class="source-pill">${escapeHTML(c.source_name)}</span></div><div class="channel-control"><button class="row-action" data-edit-channel="${c.id}">Editar</button><span class="channel-state">${c.enabled?'Activo':'Inactivo'}</span><label class="toggle" title="Incluir en la salida"><input type="checkbox" data-channel-id="${c.id}" ${c.enabled?'checked':''}><span></span></label></div></div>`).join('');$$('.channel-logo img').forEach(image=>{const loaded=()=>{image.classList.add('loaded');image.parentElement.classList.add('has-logo')};image.addEventListener('load',loaded,{once:true});image.addEventListener('error',()=>{image.parentElement.classList.remove('has-logo');image.remove()},{once:true});if(image.complete&&image.naturalWidth)loaded()})}
async function loadQuickChannels(append=false){
  if(append&&(state.quickLoading||state.quickFilterPending||state.quickChannels.length>=state.quickTotal))return;
  const requestID=++state.quickRequest;
  state.quickLoading=true;state.quickLoadError=false;state.quickFilterPending=false;renderQuickChannels();
  try{
    const query=state.quickQuery.trim();
    const offset=append?state.quickChannels.length:0;
    let url=`${api}/channels?status=enabled&order=name&limit=${state.quickMode==='recent'?10000:state.quickLimit}`;
    if(state.quickMode!=='recent'){url+=`&offset=${offset}`;if(query)url+=`&q=${encodeURIComponent(query)}`}
    const response=await fetch(url);
    const channels=await response.json().catch(()=>[]);
    if(!response.ok)throw new Error(channels.error||`Error ${response.status}`);
    if(!Array.isArray(channels))throw new Error('Respuesta de canales no válida');
    if(requestID!==state.quickRequest)return;
    if(state.quickMode==='recent'){
      const byID=new Map(channels.map(channel=>[channel.id,channel]));
      let recent=state.quickRecent.map(id=>byID.get(id)).filter(Boolean);
      if(query){const term=query.toLowerCase();recent=recent.filter(channel=>channel.name.toLowerCase().includes(term)||(channel.group||'').toLowerCase().includes(term))}
      state.quickTotal=recent.length;state.quickChannels=recent.slice(0,state.quickLimit);
    }else{
      const total=Number(response.headers.get('X-Total-Count'))||channels.length;
      state.quickChannels=append?[...new Map([...state.quickChannels,...channels].map(channel=>[channel.id,channel])).values()]:channels;
      state.quickTotal=append&&state.quickChannels.length===offset?offset:total;
    }
  }catch(error){if(requestID===state.quickRequest)state.quickLoadError=true;throw error}
  finally{if(requestID===state.quickRequest){state.quickLoading=false;renderQuickChannels();document.dispatchEvent(new Event('acebridge:catalogloaded'))}}
}
function renderQuickChannels(){const channels=state.quickChannels;const query=state.quickQuery.trim();$('#quick-channels-empty').classList.toggle('hidden',channels.length!==0);$('#quick-channel-list').classList.toggle('hidden',channels.length===0);$('#quick-empty-text').textContent=query?'No encontramos canales con esa búsqueda.':state.quickMode==='recent'?'Aún no has reproducido ningún canal.':'Aún no hay canales activos.';$('#quick-channel-list').setAttribute('aria-busy',String(state.quickLoading));$('#quick-load-status').textContent=state.quickLoading?'Cargando canales…':state.quickLoadError?'No se pudieron cargar más canales. Desplázate para reintentar.':'';$('#quick-channel-list').innerHTML=channels.map(channel=>`<button class="quick-channel" data-play-channel="${channel.id}" title="Reproducir ${escapeHTML(channel.name)}"><div class="quick-channel-visual"><span>${escapeHTML(channel.name.slice(0,1).toUpperCase())}</span>${channel.logo?`<img class="quick-logo" src="${escapeHTML(channel.logo)}" alt="" loading="lazy">`:''}<span class="quick-play" aria-hidden="true"><img src="/icons/play.svg" alt=""></span></div><strong>${escapeHTML(channel.name)}</strong><small><i></i>${escapeHTML(broadcastLabel(channel,channel.group||'Disponible'))}</small></button>`).join('');$$('.quick-channel-visual .quick-logo').forEach(image=>{const loaded=()=>{image.classList.add('loaded');image.parentElement.classList.add('has-logo')};image.addEventListener('load',loaded,{once:true});image.addEventListener('error',()=>{image.parentElement.classList.remove('has-logo');image.remove()},{once:true});if(image.complete&&image.naturalWidth)loaded()})}
function rememberChannel(id){const channelID=Number(id);state.quickRecent=[channelID,...state.quickRecent.filter(item=>item!==channelID)].slice(0,30);try{localStorage.setItem('acebridge-recent-channels',JSON.stringify(state.quickRecent))}catch{}if(state.quickMode==='recent')loadQuickChannels()}
async function loadSources(){const [sources,jobs]=await Promise.all([request(`${api}/sources`),request(`${api}/sync-jobs`)]);state.sources=sources;state.syncJobs=jobs;renderSources()}
function renderSources(){$('#sources-empty').classList.toggle('hidden',state.sources.length!==0);$('#source-list').innerHTML=state.sources.map(s=>{const redbull=s.kind==='redbull_padel';const kind=redbull?'Premier Padel':s.kind==='url'?'URL':s.kind==='manual'?'Manual':'Archivo';const detail=redbull?'Directos y repeticiones disponibles de Red Bull TV':s.url||(s.kind==='manual'?'Canales añadidos manualmente':'Archivo local');return `<article class="source-card ${redbull?'source-redbull':''}"><header><div><h3>${escapeHTML(s.name)}</h3><p title="${escapeHTML(detail)}">${escapeHTML(detail)}</p></div><span class="source-health ${s.last_status==='error'?'error':''}">${escapeHTML(sourceSyncLabel(s,state.syncJobs||[]))}</span></header><footer><small>${s.channel_count} ${s.channel_count===1?'canal':'canales'} · ${kind}</small><button class="button ghost" data-edit-source="${s.id}">Editar</button>${s.kind==='url'||redbull?`<button class="button ghost" data-sync="${s.id}" ${sourceSyncRunning(s.id,state.syncJobs||[])?'disabled':''}>Sincronizar</button>`:''}<button class="button danger" data-delete="${s.id}">Eliminar</button></footer>${s.last_error?`<p class="source-health error">${escapeHTML(s.last_error)}</p>`:''}</article>`}).join('')}
async function loadSettings(){state.settings=await request(`${api}/settings`);$('#engine-url').value=state.settings.engine_url;$('#public-url').value=state.settings.public_base_url||'';$('#update-interval').value=String(state.settings.update_interval_hours);$('#max-streams').value=state.settings.max_streams||4;$('#stream-idle').value=state.settings.stream_idle_seconds||60;$('#history-days').value=state.settings.history_retention_days||90;$('#diagnostics-enabled').checked=Boolean(state.settings.diagnostics_enabled);const base=state.settings.public_base_url||location.origin;$('#playlist-url').value=`${base.replace(/\/$/,'')}/playlist.m3u`;document.dispatchEvent(new Event('acebridge:settingsloaded'));await loadEPG()}
function toggleEPGProxy(){const enabled=$('#epg-use-proxy').checked;$('#epg-proxy-wrap').classList.toggle('hidden',!enabled);$('#epg-proxy-url').required=enabled;if(!enabled)$('#epg-proxy-url').value=''}
async function loadEPG(){const config=await request(`${api}/epg`);$('#epg-enabled').checked=config.enabled;$('#epg-source-url').value=config.url||'';$('#epg-source-url').required=config.enabled;$('#epg-use-proxy').checked=Boolean(config.proxy_url);$('#epg-proxy-url').value=config.proxy_url||'';toggleEPGProxy();const hours=String(config.cache_hours||6);if(![...$('#epg-cache-hours').options].some(option=>option.value===hours))$('#epg-cache-hours').add(new Option(`Cada ${hours} horas`,hours));$('#epg-cache-hours').value=hours;$('#epg-output-url').value=`${(state.settings?.public_base_url||location.origin).replace(/\/$/,'')}/epg.xml`;$('#refresh-epg').disabled=!config.enabled;$('#epg-status').textContent=!config.enabled?'EPG desactivada.':config.last_error?`Última actualización fallida: ${config.last_error}${config.cached_at?' · Se conserva la última guía válida.':''}`:config.cached_at?`Guía disponible · ${Math.round(config.cached_bytes/1024)} KB · Actualizada ${relativeTime(config.cached_at)}`:'Sin caché todavía. Pulsa Actualizar ahora o abre la URL XMLTV.'}
$('#epg-use-proxy').addEventListener('change',toggleEPGProxy);
$('#epg-enabled').addEventListener('change',()=>{$('#epg-source-url').required=$('#epg-enabled').checked});
$('#epg-form').addEventListener('submit',async event=>{event.preventDefault();const button=event.submitter||$('#epg-form button[type="submit"]');button.disabled=true;try{await request(`${api}/epg`,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({enabled:$('#epg-enabled').checked,url:$('#epg-source-url').value,proxy_url:$('#epg-use-proxy').checked?$('#epg-proxy-url').value:'',cache_hours:Number($('#epg-cache-hours').value)})});await loadEPG();toast('Configuración EPG guardada')}catch(error){toast(error.message,true)}finally{button.disabled=false}});
$('#refresh-epg').addEventListener('click',async()=>{const button=$('#refresh-epg');button.disabled=true;$('#epg-status').textContent='Descargando y validando la guía…';try{await request(`${api}/epg/refresh`,{method:'POST'});toast('EPG actualizada')}catch(error){toast(error.message,true)}finally{await loadEPG().catch(error=>toast(error.message,true))}});
$('#copy-epg').addEventListener('click',()=>copyField('#epg-output-url','URL EPG copiada'));
function updateMonitorIdleCopy(){const seconds=state.settings?.stream_idle_seconds||60;$('#monitor-idle-copy').textContent=`Se liberan automáticamente tras ${seconds} segundos sin actividad.`}
async function refreshMonitorLive(){if(state.monitorLoading||document.hidden||!$('#view-monitor').classList.contains('active'))return;updateMonitorIdleCopy();state.monitorLoading=true;try{await loadMonitor()}catch(error){console.error('Monitor refresh failed',error)}finally{state.monitorLoading=false}}
async function refreshAll(){try{await Promise.all([loadHealth(),loadDashboard(),loadChannels(),loadQuickChannels(),loadSources(),loadSettings(),refreshMonitorLive()]);updateMonitorIdleCopy()}catch(error){toast(error.message,true)}}
function setSourceMode(mode){
  state.sourceMode=mode;
  for(const [key,selector] of Object.entries({url:'#source-url-fields',file:'#source-file-fields',manual:'#source-manual-fields',redbull:'#source-redbull-fields'})){
    const active=key===mode;
    $(selector).classList.toggle('hidden',!active);
    $(selector).querySelectorAll('input,select').forEach(input=>{input.disabled=!active});
  }
  $('#source-url').required=mode==='url';
  $('#source-file').required=mode==='file';
  $('#manual-ace-id').required=mode==='manual';
  $('#source-proxy-url').required=mode==='url'&&$('#source-use-proxy').checked;
  $$('[data-source-tab]').forEach(x=>{x.classList.toggle('active',x.dataset.sourceTab===mode);x.setAttribute('aria-pressed',String(x.dataset.sourceTab===mode))});
  $('#source-name-label').textContent=mode==='manual'?'Nombre del canal':'Nombre de la fuente';
  $('#source-name').placeholder=mode==='manual'?'Mi canal exclusivo':mode==='redbull'?'Red Bull Premier Padel':'Canales deportivos';
  if(mode==='redbull'&&!$('#source-name').value)$('#source-name').value='Red Bull Premier Padel';
  $('#submit-source').textContent=mode==='manual'?'Añadir canal':mode==='redbull'?'Añadir Premier Padel':'Importar fuente';
}
function openSourceDialog(mode='url'){$('#source-form').reset();toggleSourceProxy('');setSourceMode(mode);$('#source-dialog').showModal()}
function openChannelEditor(id){const channel=state.channels.find(item=>item.id===Number(id));if(!channel)return;$('#edit-channel-id').value=channel.id;$('#edit-channel-name').value=channel.name;$('#edit-channel-ace-id').value=channel.acestream_id;$('#edit-channel-stream-wrap').classList.toggle('hidden',channel.stream_type!=='acestream');$('#edit-channel-ace-id').required=channel.stream_type==='acestream';$('#edit-channel-group').value=channel.group||'';$('#edit-channel-tvg-id').value=channel.tvg_id||'';$('#edit-channel-logo').value=channel.logo||'';$('#channel-edit-dialog').showModal()}
function toggleSourceProxy(prefix){const enabled=$(`#${prefix}source-use-proxy`).checked;$(`#${prefix}source-proxy-url`).required=enabled;$(`#${prefix}source-proxy-wrap`).classList.toggle('hidden',!enabled);if(!enabled)$(`#${prefix}source-proxy-url`).value=''}
function openSourceEditor(id){const source=state.sources.find(item=>item.id===Number(id));if(!source)return;$('#edit-source-id').value=source.id;$('#edit-source-name').value=source.name;$('#edit-source-url').value=source.url||'';$('#edit-source-proxy-url').value=source.proxy_url||'';$('#edit-source-use-proxy').checked=Boolean(source.proxy_url);toggleSourceProxy('edit-');$('#edit-source-enabled').checked=source.enabled;$('#edit-source-url-wrap').classList.toggle('hidden',source.kind!=='url');$('#source-edit-dialog').showModal()}
function releasePlayerSession(){const session=state.playerSession;state.playerSession=null;clearInterval(state.playerHeartbeat);state.playerHeartbeat=null;if(session)fetch(`${api}/channels/${session.channelID}/viewers/${encodeURIComponent(session.viewerID)}`,{method:'DELETE',keepalive:true}).catch(()=>{})}
function closePlayer(){state.playerToken++;releasePlayerSession();const video=$('#channel-player');video.pause();video.removeAttribute('src');video.load();if(state.playerHLS){state.playerHLS.destroy();state.playerHLS=null}if($('#player-dialog').open)$('#player-dialog').close()}
$('#player-dialog').addEventListener('cancel',event=>{event.preventDefault();closePlayer()});
$('#player-dialog').addEventListener('close',()=>{if(!$('#player-dialog').open&&(state.playerSession||state.playerHLS||$('#channel-player').hasAttribute('src')))closePlayer()});
window.addEventListener('pagehide',closePlayer);
function trackPlayerSession(channelID,viewerID){if(!viewerID)return;state.playerSession={channelID,viewerID};state.playerHeartbeat=setInterval(()=>{const session=state.playerSession;if(session)fetch(`${api}/channels/${session.channelID}/viewers/${encodeURIComponent(session.viewerID)}/heartbeat`,{method:'POST',keepalive:true}).catch(()=>{})},15000)}
async function openPlayer(id,retry=0){
  const channel=state.channels.find(item=>item.id===Number(id));
  if(!channel)return;
  closePlayer();
  const token=state.playerToken;
  const video=$('#channel-player');
  const current=()=>token===state.playerToken&&$('#player-dialog').open;
  const status=message=>{if(current())$('#player-status').textContent=message};
  $('#player-title').textContent=channel.name;
  $('#player-meta').textContent=`${channel.group||'Sin grupo'} · ${channel.source_name}`;
  video.poster=channel.logo||'';
  $('#open-stream').href=`${api}/channels/${channel.id}/stream`;
  $('#player-dialog').showModal();
  status(channel.stream_type==='acestream'?'Ace Engine está preparando la señal…':'Preparando señal IPTV…');
  let playback;
  try{
    playback=await request(`${api}/channels/${channel.id}/playback`,{method:'POST'});
    if(!current()){
      if(playback.viewer_id)fetch(`${api}/channels/${channel.id}/viewers/${encodeURIComponent(playback.viewer_id)}`,{method:'DELETE',keepalive:true}).catch(()=>{});
      return;
    }
  }catch(error){status(`No se pudo preparar la señal: ${error.message}`);return}
  const browserURL=playback.proxy_url;
  trackPlayerSession(channel.id,playback.viewer_id);
  $('#open-stream').href=browserURL;
  status('Señal preparada. Iniciando reproducción…');
  const play=()=>{if(current())video.play().catch(()=>status('La señal está lista. Pulsa reproducir.'))};
  if(playback.stream_type==='mpegts'){status('Señal MPEG-TS disponible. Usa Abrir stream con VLC o Jellyfin.');return}
  if(playback.stream_type==='native'||video.canPlayType('application/vnd.apple.mpegurl')){video.src=browserURL;play();return}
  if(!window.Hls||!Hls.isSupported()){status('Este navegador no admite reproducción HLS.');return}
  const hls=new Hls({enableWorker:true,lowLatencyMode:false,startFragPrefetch:true,liveSyncDurationCount:3,manifestLoadingTimeOut:90000,manifestLoadingMaxRetry:3,levelLoadingTimeOut:90000,fragLoadingTimeOut:90000});
  state.playerHLS=hls;
  let started=false,mediaRecoveries=0;
  const startWhenVideoReady=()=>{
    if(!current()||started||video.readyState<2||(!video.videoWidth&&!video.videoHeight))return;
    started=true;play();
  };
  // Remove listeners with this HLS instance, including close-during-load.
  video.addEventListener('loadeddata',startWhenVideoReady);
  hls.on(Hls.Events.DESTROYING,()=>video.removeEventListener('loadeddata',startWhenVideoReady));
  hls.on(Hls.Events.MANIFEST_PARSED,()=>status('Señal preparada. Creando búfer de vídeo…'));
  hls.on(Hls.Events.FRAG_BUFFERED,startWhenVideoReady);
  hls.on(Hls.Events.ERROR,(_,data)=>{
    if(!current()||!data.fatal)return;
    if(data.type===Hls.ErrorTypes.MEDIA_ERROR&&mediaRecoveries<2){mediaRecoveries++;status('Recuperando la imagen…');hls.recoverMediaError();return}
    if(data.type===Hls.ErrorTypes.NETWORK_ERROR&&retry<2){status('Renovando la señal…');setTimeout(()=>{if(current())openPlayer(id,retry+1)},700);return}
    status(`No se pudo reproducir la señal${data.details?` (${data.details})`:''}. Usa “Abrir stream”.`);
  });
  hls.loadSource(browserURL);hls.attachMedia(video);
}

$('#menu-button').addEventListener('click',event=>{event.stopPropagation();toggleMobileMenu()});
$('#sidebar-backdrop').addEventListener('click',closeMobileMenu);
document.addEventListener('click',event=>{const button=event.target.closest('#quick-channel-list [data-play-channel]');if(!button)return;const channel=state.quickChannels.find(item=>item.id===Number(button.dataset.playChannel));if(channel&&!state.channels.some(item=>item.id===channel.id))state.channels.push(channel)},{capture:true});
document.addEventListener('keydown',event=>{if(event.key==='Escape')closeMobileMenu()});
window.addEventListener('resize',()=>{if(window.innerWidth>720)closeMobileMenu()});
document.addEventListener('click',event=>{const closePlayerButton=event.target.closest('[data-close-player]');if(closePlayerButton){closePlayer();return}const close=event.target.closest('[data-close-dialog]');if(close){document.getElementById(close.dataset.closeDialog).close();return}const channelPlay=event.target.closest('[data-play-channel]');if(channelPlay){openPlayer(channelPlay.dataset.playChannel);return}const channelEdit=event.target.closest('[data-edit-channel]');if(channelEdit){openChannelEditor(channelEdit.dataset.editChannel);return}const sourceEdit=event.target.closest('[data-edit-source]');if(sourceEdit){openSourceEditor(sourceEdit.dataset.editSource)}});

document.addEventListener('click',async event=>{const nav=event.target.closest('[data-view]');if(nav){showView(nav.dataset.view);return}if(event.target.closest('#menu-button')){$('.sidebar').classList.toggle('open');return}if(event.target.closest('#refresh-button')){await refreshAll();toast('Datos actualizados');return}if(event.target.closest('#add-channel')){openSourceDialog('manual');return}if(event.target.closest('#add-source')||event.target.closest('[data-open-source]')){openSourceDialog();return}if(event.target.closest('#cancel-source')||event.target.closest('#close-source')){$('#source-dialog').close();return}const tab=event.target.closest('[data-source-tab]');if(tab){setSourceMode(tab.dataset.sourceTab);return}const sync=event.target.closest('[data-sync]');if(sync){sync.disabled=true;try{await request(`${api}/sources/${sync.dataset.sync}/sync-jobs`,{method:'POST'});await loadSources();toast('Sincronización iniciada')}catch(error){toast(error.message,true)}finally{sync.disabled=false}return}const remove=event.target.closest('[data-delete]');if(remove&&confirm('¿Eliminar esta fuente y sus canales?')){try{await request(`${api}/sources/${remove.dataset.delete}`,{method:'DELETE'});await refreshAll();toast('Fuente eliminada')}catch(error){toast(error.message,true)}return}if(event.target.closest('#copy-playlist')){await copyField('#playlist-url')}if(event.target.closest('#check-engine')){const button=event.target.closest('#check-engine');button.disabled=true;try{const data=await request(`${api}/engine/check`,{method:'POST'});$('#engine-dot').className=data.operational?'ok':'error';$('#engine-label').textContent=data.operational?`Operativo · v${data.version}`:data.message}catch(error){$('#engine-dot').className='error';$('#engine-label').textContent='Error al comprobar'}finally{button.disabled=false}}});
$('#source-use-proxy').addEventListener('change',()=>toggleSourceProxy(''));
$('#edit-source-use-proxy').addEventListener('change',()=>toggleSourceProxy('edit-'));
$('#channel-search').addEventListener('input',renderChannels);
$('#channel-group').addEventListener('change',renderChannels);
function resetQuickCatalog(){state.quickLimit=60;state.quickLoadError=false;state.quickFilterPending=true;state.quickLoading=false;++state.quickRequest;$('.quick-catalog-scroll').scrollTop=0;$('.quick-channels').scrollTop=0}
$('#quick-channel-search').addEventListener('input',event=>{state.quickQuery=event.target.value;resetQuickCatalog();clearTimeout(loadQuickChannels.timer);loadQuickChannels.timer=setTimeout(()=>loadQuickChannels().catch(error=>toast(error.message,true)),220)});
document.addEventListener('click',event=>{const mode=event.target.closest('[data-quick-mode]');if(mode){state.quickMode=mode.dataset.quickMode;resetQuickCatalog();$$('[data-quick-mode]').forEach(button=>button.classList.toggle('active',button===mode));loadQuickChannels().catch(error=>toast(error.message,true));return}},{capture:true});
document.addEventListener('click',event=>{const channel=event.target.closest('[data-play-channel]');if(channel)rememberChannel(channel.dataset.playChannel)},{capture:true});
$('#channel-player').addEventListener('playing',()=>{$('#player-status').textContent='En directo'});$('#channel-player').addEventListener('waiting',()=>{$('#player-status').textContent='Cargando señal…'});$('#player-dialog').addEventListener('click',event=>{if(event.target===$('#player-dialog'))closePlayer()});
$('#channel-list').addEventListener('change',async event=>{const input=event.target.closest('[data-channel-id]');if(!input)return;try{await request(`${api}/channels/${input.dataset.channelId}`,{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify({enabled:input.checked})});const channel=state.channels.find(c=>c.id===Number(input.dataset.channelId));if(channel)channel.enabled=input.checked;const row=input.closest('.channel-row');row.querySelector('.channel-state').textContent=input.checked?'Activo':'Inactivo';const availability=row.querySelector('.channel-copy small');availability.innerHTML=`<i></i>${input.checked?'Disponible':'Oculto'} · ${escapeHTML(channel?.tvg_id||channel?.acestream_id.slice(0,10)+'…'||'')}`;await loadDashboard()}catch(error){input.checked=!input.checked;toast(error.message,true)}});
$('#source-form').addEventListener('submit',async event=>{event.preventDefault();const button=event.submitter;button.disabled=true;try{if(state.sourceMode==='url'){await request(`${api}/sources/url`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:$('#source-name').value,url:$('#source-url').value,proxy_url:$('#source-use-proxy').checked?$('#source-proxy-url').value:''})})}else if(state.sourceMode==='file'){const file=$('#source-file').files[0];if(!file)throw new Error('Selecciona un archivo M3U o JSON');const form=new FormData();form.append('name',$('#source-name').value);form.append('file',file);await request(`${api}/sources/file`,{method:'POST',body:form})}else if(state.sourceMode==='redbull'){await request(`${api}/sources/redbull-padel`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:$('#source-name').value})})}else{await request(`${api}/channels/manual`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:$('#source-name').value,acestream_id:$('#manual-ace-id').value,group:$('#manual-group').value,logo:$('#manual-logo').value})})}$('#source-dialog').close();event.target.reset();await refreshAll();showView(state.sourceMode==='manual'?'channels':'sources');toast(state.sourceMode==='manual'?'Canal añadido':state.sourceMode==='redbull'?'Premier Padel añadido':'Fuente importada')}catch(error){toast(error.message,true)}finally{button.disabled=false}});
$('#settings-form').addEventListener('submit',async event=>{event.preventDefault();const button=event.submitter;button.disabled=true;try{state.settings=await request(`${api}/settings`,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({engine_url:$('#engine-url').value,public_base_url:$('#public-url').value,update_interval_hours:Number($('#update-interval').value),max_streams:Number($('#max-streams').value),stream_idle_seconds:Number($('#stream-idle').value),history_retention_days:Number($('#history-days').value),diagnostics_enabled:$('#diagnostics-enabled').checked})});await loadSettings();toast('Configuración guardada')}catch(error){toast(error.message,true)}finally{button.disabled=false}});
$('#channel-edit-form').addEventListener('submit',async event=>{event.preventDefault();const button=event.submitter;button.disabled=true;try{const channel=state.channels.find(item=>item.id===Number($('#edit-channel-id').value));const payload={name:$('#edit-channel-name').value,group:$('#edit-channel-group').value,tvg_id:$('#edit-channel-tvg-id').value,logo:$('#edit-channel-logo').value};if(channel?.stream_type==='acestream')payload.acestream_id=$('#edit-channel-ace-id').value;await request(`${api}/channels/${$('#edit-channel-id').value}`,{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});$('#channel-edit-dialog').close();await Promise.all([loadChannels(),loadDashboard()]);toast('Canal actualizado')}catch(error){toast(error.message,true)}finally{button.disabled=false}});
$('#source-edit-form').addEventListener('submit',async event=>{event.preventDefault();const button=event.submitter;button.disabled=true;try{const source=state.sources.find(item=>item.id===Number($('#edit-source-id').value));const payload={name:$('#edit-source-name').value,enabled:$('#edit-source-enabled').checked};if(source?.kind==='url'){payload.url=$('#edit-source-url').value;payload.proxy_url=$('#edit-source-use-proxy').checked?$('#edit-source-proxy-url').value:''}await request(`${api}/sources/${$('#edit-source-id').value}`,{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});$('#source-edit-dialog').close();await Promise.all([loadSources(),loadChannels()]);toast('Fuente actualizada')}catch(error){toast(error.message,true)}finally{button.disabled=false}});
document.addEventListener('click',async event=>{const button=event.target.closest('[data-refresh-monitor]');if(!button)return;button.disabled=true;try{await refreshMonitorLive();toast('Monitor actualizado')}catch(error){toast(error.message,true)}finally{button.disabled=false}});
window.addEventListener('hashchange',()=>showView(location.hash.slice(1)||'overview',false));
document.addEventListener('visibilitychange',()=>{if(!document.hidden)refreshMonitorLive()});
setInterval(refreshMonitorLive,5000);
setInterval(refreshSourceSync,3000);
document.addEventListener('visibilitychange',()=>{if(!document.hidden)refreshSourceSync()});
const initial=location.hash.slice(1);showView(views[initial]?initial:'overview',false);refreshAll();

function hydrateMonitorChannelLogos(){
  const groups=[{selector:'#monitor-stream-list .monitor-row',items:state.monitor.streams},{selector:'#monitor-history-list .monitor-row',items:state.monitor.history}];
  groups.forEach(group=>{$$(group.selector).forEach((row,index)=>{const logo=group.items[index]?.channel_logo;const mark=row.querySelector('.monitor-channel-mark');if(!logo||!mark||mark.querySelector('img'))return;const image=document.createElement('img');image.src=logo;image.alt='';image.loading='lazy';image.addEventListener('load',()=>mark.classList.add('has-logo'),{once:true});image.addEventListener('error',()=>image.remove(),{once:true});mark.appendChild(image);if(image.complete&&image.naturalWidth)mark.classList.add('has-logo')})});
}
new MutationObserver(hydrateMonitorChannelLogos).observe($('#view-monitor'),{childList:true,subtree:true});
