const channelUI={page:0,limit:50,total:0,query:'',group:'',status:'',order:'name',selected:new Set(),loading:false,diagnosticID:null,searchTimer:null};

async function managedChannelRequest(){
  const params=new URLSearchParams({limit:String(channelUI.limit),offset:String(channelUI.page*channelUI.limit),order:channelUI.order});
  if(channelUI.query)params.set('q',channelUI.query);
  if(channelUI.group)params.set('group',channelUI.group);
  if(channelUI.status)params.set('status',channelUI.status);
  const response=await fetch(`${api}/channels?${params}`);
  const data=await response.json().catch(()=>[]);
  if(!response.ok)throw new Error(data.error||`Error ${response.status}`);
  channelUI.total=Number(response.headers.get('X-Total-Count'))||0;
  return data;
}

async function managedLoadGroups(){
  const groups=await request(`${api}/channel-groups`);
  const groupSelect=$('#channel-group');
  const bulkSelect=$('#bulk-channel-group');
  const options=groups.map(item=>`<option value="${escapeHTML(item.name)}">${escapeHTML(item.name||'Sin grupo')} (${item.count})</option>`).join('');
  groupSelect.innerHTML='<option value="">Todos los grupos</option>'+options;
  groupSelect.value=channelUI.group;
  bulkSelect.innerHTML='<option value="">Cambiar grupo…</option>'+groups.map(item=>`<option value="${escapeHTML(item.name)}">${escapeHTML(item.name||'Sin grupo')}</option>`).join('');
}

async function managedLoadChannels(){
  if(channelUI.loading)return;
  channelUI.loading=true;
  $('#channel-list').classList.add('is-loading');
  try{
    const channels=await managedChannelRequest();
    state.channels=channels;
    if(channelUI.page>0&&channels.length===0){channelUI.page--;channelUI.loading=false;return managedLoadChannels()}
    managedRenderChannels();
  }finally{
    channelUI.loading=false;
    $('#channel-list').classList.remove('is-loading');
  }
}

function managedRenderChannels(){
  const channels=state.channels;
  $('#channel-count').textContent=`${channelUI.total} ${channelUI.total===1?'canal':'canales'}`;
  $('#channels-empty').classList.toggle('hidden',channels.length!==0);
  $('.channel-table-head').classList.toggle('hidden',channels.length===0);
  $('#channel-pagination').classList.toggle('hidden',channelUI.total<=channelUI.limit);
  const pages=Math.max(1,Math.ceil(channelUI.total/channelUI.limit));
  $('#channel-page-label').textContent=`Página ${channelUI.page+1} de ${pages} · ${channelUI.total} canales`;
  $('#channel-prev').disabled=channelUI.page===0;
  $('#channel-next').disabled=channelUI.page>=pages-1;
  $('#channel-list').innerHTML=channels.map(channel=>`<div class="channel-row"><div class="channel-main-cell"><label class="channel-select" title="Seleccionar"><input type="checkbox" data-select-channel="${channel.id}" ${channelUI.selected.has(channel.id)?'checked':''}><span></span></label><button class="channel-identity" data-play-channel="${channel.id}" title="Reproducir ${escapeHTML(channel.name)}"><div class="channel-logo"><span>${escapeHTML(channel.name.slice(0,1).toUpperCase())}</span>${channel.logo?`<img src="${escapeHTML(channel.logo)}" alt="" loading="lazy">`:''}</div><div class="channel-copy"><strong>${escapeHTML(channel.name)}</strong><small><i></i>${escapeHTML(channel.enabled?'Disponible':'Oculto')} · ${escapeHTML(channel.tvg_id||channel.acestream_id.slice(0,10)+'…')}</small></div><span class="play-hint">Reproducir</span></button></div><div class="channel-group"><span class="tag">${escapeHTML(channel.group||'Sin grupo')}</span></div><div class="channel-source"><span class="source-pill">${escapeHTML(channel.source_name)}</span></div><div class="channel-control pro"><button class="row-icon-action" data-diagnose-channel="${channel.id}" title="Comprobar señal"><img src="/icons/activity.svg" alt=""></button><button class="row-action" data-edit-channel="${channel.id}">Editar</button>${channel.source_kind==='manual'?`<button class="row-icon-action danger" data-delete-manual="${channel.id}" title="Eliminar canal">×</button>`:''}<div class="channel-output-control"><span class="channel-state">${channel.enabled?'Activo':'Inactivo'}</span><label class="toggle" title="Incluir en la salida"><input type="checkbox" data-channel-id="${channel.id}" ${channel.enabled?'checked':''}><span></span></label></div></div></div>`).join('');
  $$('.channel-logo img').forEach(image=>{const loaded=()=>{image.classList.add('loaded');image.parentElement.classList.add('has-logo')};image.addEventListener('load',loaded,{once:true});image.addEventListener('error',()=>{image.parentElement.classList.remove('has-logo');image.remove()},{once:true});if(image.complete&&image.naturalWidth)loaded()});
  updateBulkBar();
}

function updateBulkBar(){
  const count=channelUI.selected.size;
  $('#bulk-channel-bar').classList.toggle('hidden',count===0);
  $('#selected-channel-count').textContent=`${count} ${count===1?'seleccionado':'seleccionados'}`;
  const pageIDs=state.channels.map(channel=>channel.id);
  const checked=pageIDs.length>0&&pageIDs.every(id=>channelUI.selected.has(id));
  $('#select-page-channels').checked=checked;
  $('#select-page-channels').indeterminate=!checked&&pageIDs.some(id=>channelUI.selected.has(id));
}

async function applyBulkChange(payload,message){
  const ids=[...channelUI.selected];
  if(!ids.length)return;
  const result=await request(`${api}/channels/bulk`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({ids,...payload})});
  if(payload.diagnose){
    const failed=result.diagnostics.filter(item=>item.status==='error').length;
    toast(failed?`${ids.length-failed} señales correctas · ${failed} con error`:`${ids.length} señales comprobadas`,failed>0);
  }else toast(message);
  channelUI.selected.clear();
  await Promise.all([managedLoadChannels(),loadDashboard()]);
}

async function runChannelDiagnostic(id){
  const channel=state.channels.find(item=>item.id===Number(id));
  if(!channel)return;
  channelUI.diagnosticID=channel.id;
  $('#diagnostic-title').textContent=channel.name;
  $('#diagnostic-summary').textContent='Ace Engine está preparando y analizando la señal…';
  $('#diagnostic-result').innerHTML='<div class="diagnostic-loading"><i></i><span>Comprobando manifiesto y metadatos</span></div>';
  if(!$('#diagnostic-dialog').open)$('#diagnostic-dialog').showModal();
  try{
    const result=await request(`${api}/channels/${channel.id}/diagnose`,{method:'POST'});
    const healthy=result.status==='healthy';
    $('#diagnostic-summary').textContent=healthy?'La señal está disponible y responde correctamente.':result.error||'No se pudo preparar la señal.';
    $('#diagnostic-result').innerHTML=`<div class="diagnostic-status ${healthy?'ok':'error'}"><strong>${healthy?'Señal operativa':'Señal con problemas'}</strong><span>${result.latency_ms} ms</span></div><div class="diagnostic-grid"><article><small>Resolución</small><strong>${escapeHTML(result.resolution||'No detectada')}</strong></article><article><small>Códecs</small><strong>${escapeHTML(result.codecs||'No detectados')}</strong></article><article><small>Bitrate</small><strong>${result.bitrate?`${Math.round(result.bitrate/1000)} kbps`:'No detectado'}</strong></article><article><small>Logo</small><strong>${result.logo_accessible?'Accesible':result.logo?'No accesible':'Sin logo'}</strong></article><article><small>Duplicados</small><strong>${result.duplicates||0}</strong></article><article><small>Preparación</small><strong>${result.latency_ms} ms</strong></article></div>`;
    await loadDashboard();
  }catch(error){
    $('#diagnostic-summary').textContent=error.message;
    $('#diagnostic-result').innerHTML='<div class="diagnostic-status error"><strong>Diagnóstico fallido</strong><span>Revisa Ace Engine</span></div>';
  }
}

const baseRenderSources=renderSources;
renderSources=function(){
  baseRenderSources();
  state.sources.forEach((source,index)=>{
    if(source.kind!=='file')return;
    const card=$$('.source-card')[index];
    if(!card)return;
    const footer=card.querySelector('footer');
    const button=document.createElement('button');
    button.className='button ghost';button.type='button';button.dataset.replaceFile=source.id;button.textContent='Reemplazar';
    footer.insertBefore(button,footer.querySelector('[data-delete]'));
  });
};

loadChannels=managedLoadChannels;
renderChannels=managedRenderChannels;

const oldSearch=$('#channel-search');const newSearch=oldSearch.cloneNode(true);oldSearch.replaceWith(newSearch);
const oldGroup=$('#channel-group');const newGroup=oldGroup.cloneNode(true);oldGroup.replaceWith(newGroup);
newSearch.addEventListener('input',event=>{clearTimeout(channelUI.searchTimer);channelUI.searchTimer=setTimeout(()=>{channelUI.query=event.target.value.trim();channelUI.page=0;managedLoadChannels()},260)});
newGroup.addEventListener('change',event=>{channelUI.group=event.target.value;channelUI.page=0;managedLoadChannels()});
$('#channel-status').addEventListener('change',event=>{channelUI.status=event.target.value;channelUI.page=0;managedLoadChannels()});
$('#channel-order').addEventListener('change',event=>{channelUI.order=event.target.value;channelUI.page=0;managedLoadChannels()});
$('#channel-prev').addEventListener('click',()=>{if(channelUI.page>0){channelUI.page--;managedLoadChannels()}});
$('#channel-next').addEventListener('click',()=>{if((channelUI.page+1)*channelUI.limit<channelUI.total){channelUI.page++;managedLoadChannels()}});
$('#select-page-channels').addEventListener('change',event=>{state.channels.forEach(channel=>event.target.checked?channelUI.selected.add(channel.id):channelUI.selected.delete(channel.id));managedRenderChannels()});
$('#bulk-channel-group').addEventListener('change',async event=>{const group=event.target.value;event.target.value='';if(group)await applyBulkChange({group},'Grupo actualizado')});
$('#repeat-diagnostic').addEventListener('click',()=>runChannelDiagnostic(channelUI.diagnosticID));

document.addEventListener('change',event=>{const box=event.target.closest('[data-select-channel]');if(!box)return;const id=Number(box.dataset.selectChannel);box.checked?channelUI.selected.add(id):channelUI.selected.delete(id);updateBulkBar()});
document.addEventListener('click',async event=>{
  const enabled=event.target.closest('[data-bulk-enabled]');if(enabled){await applyBulkChange({enabled:enabled.dataset.bulkEnabled==='true'},enabled.dataset.bulkEnabled==='true'?'Canales activados':'Canales ocultos');return}
  if(event.target.closest('[data-bulk-diagnose]')){await applyBulkChange({diagnose:true},'Señales comprobadas');return}
  if(event.target.closest('[data-clear-selection]')){channelUI.selected.clear();managedRenderChannels();return}
  const diagnose=event.target.closest('[data-diagnose-channel]');if(diagnose){await runChannelDiagnostic(diagnose.dataset.diagnoseChannel);return}
  const remove=event.target.closest('[data-delete-manual]');if(remove&&confirm('¿Eliminar definitivamente este canal manual?')){await request(`${api}/channels/${remove.dataset.deleteManual}`,{method:'DELETE'});await managedLoadChannels();toast('Canal eliminado');return}
  const replace=event.target.closest('[data-replace-file]');if(replace){const input=document.createElement('input');input.type='file';input.accept='.m3u,.m3u8,.json';input.addEventListener('change',async()=>{if(!input.files[0])return;const form=new FormData();form.append('file',input.files[0]);try{await request(`${api}/sources/${replace.dataset.replaceFile}/file`,{method:'PUT',body:form});await Promise.all([loadSources(),managedLoadChannels(),managedLoadGroups()]);toast('Fuente reemplazada correctamente')}catch(error){toast(error.message,true)}});input.click()}
});

managedLoadGroups().then(managedLoadChannels).catch(error=>toast(error.message,true));
