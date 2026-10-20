// Dependency-free frontend regressions: node --test scripts/workspace.test.cjs
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const web = path.join(__dirname, '../internal/app/web');
const app = fs.readFileSync(path.join(web, 'app.js'), 'utf8');

test('output and settings controls use themed styling without replacing semantic inputs', () => {
  const css=fs.readFileSync(path.join(web,'workspace.css'),'utf8');
  const html=fs.readFileSync(path.join(web,'index.html'),'utf8');
  assert.match(css, /#view-output button,#view-settings button,#view-output select,#view-settings select\{-webkit-appearance:none;appearance:none/);
  assert.match(css, /input\[type=checkbox\]::before/);
  assert.match(css, /input:checked::before\{visibility:visible\}/);
  for(const id of ['epg-enabled','epg-use-proxy','diagnostics-enabled'])assert.match(html,new RegExp(`id="${id}" type="checkbox"`));
});

test('output and settings forms share their main frame without nested white cards', () => {
  const css=fs.readFileSync(path.join(web,'workspace.css'),'utf8');
  assert.match(css, /#view-output>\.panel,#view-settings>\.panel\{background:transparent;border:0;border-radius:0;box-shadow:none;padding:0/);
  assert.match(css, /#view-output\.active\{display:grid;grid-template-columns:minmax\(0,1fr\) minmax\(0,1\.15fr\)/);
  assert.match(css, /#view-output \.check-row,#view-settings \.check-row\{background:transparent/);
});

test('channel hover uses an icon instead of text squeezed into the group column', () => {
  const managed=fs.readFileSync(path.join(web,'management-ui.js'),'utf8');
  const css=fs.readFileSync(path.join(web,'workspace.css'),'utf8');
  for(const source of [app,managed]){
    assert.doesNotMatch(source, /class="play-hint">Reproducir/);
    assert.match(source, /class="play-hint" aria-hidden="true"><img src="\/icons\/play.svg"/);
  }
  assert.match(css, /column-gap:24px/);
  assert.match(css, /\.channel-copy\{flex:1;min-width:0\}/);
  assert.match(managed, /broadcastLabel\(channel,'Disponible'\)/);
});

test('overview controls span both columns before catalog and inspector', () => {
  const html=fs.readFileSync(path.join(web,'index.html'),'utf8');
  const controls=html.indexOf('<div class="overview-controls">');
  const workspace=html.indexOf('<div class="overview-workspace">');
  assert.ok(controls>0&&workspace>controls);
  assert.doesNotMatch(html, /<h2>Canales disponibles<\/h2>/);
  assert.ok(html.indexOf('class="quick-tools"',controls)<workspace);
  assert.ok(html.indexOf('class="quick-catalog-scroll"')>workspace);
  assert.doesNotMatch(html, /TU BIBLIOTECA/);
});

test('system health lives in one compact header disclosure, not a catalog footer', () => {
  const html=fs.readFileSync(path.join(web,'index.html'),'utf8');
  assert.match(html, /<details class="sidebar-status system-status">/);
  const summary=html.match(/<summary aria-label="Estado del sistema y Ace Engine">([\s\S]*?)<\/summary>/)[1];
  assert.doesNotMatch(summary, /Sin comprobar|id="engine-label"|id="health-text"/);
  assert.match(summary, /<small>Sistema<\/small>/);
  assert.match(summary, /<small>Engine<\/small>/);
  assert.doesNotMatch(html, /class="system-strip"/);
  for(const id of ['health-dot','health-text','engine-dot','engine-label','database-status','check-engine']){
    assert.equal((html.match(new RegExp(`id="${id}"`,'g'))||[]).length,1,`${id} must remain unique`);
  }
  assert.match(app, /\$\('#database-status'\)\.textContent='Operativa'/);
  assert.match(app, /\$\('#database-status'\)\.textContent='Sin conexión'/);
});

test('desktop catalog has a viewport bound and a keyboard-accessible internal scroller', () => {
  const css=fs.readFileSync(path.join(web,'workspace.css'),'utf8');
  const html=fs.readFileSync(path.join(web,'index.html'),'utf8');
  const workspace=fs.readFileSync(path.join(web,'workspace.js'),'utf8');
  assert.match(css, /height:calc\(100dvh - var\(--overview-offset,220px\) - 24px\)/);
  assert.match(css, /\.quick-catalog-scroll\{flex:1;min-height:0;overflow:auto/);
  assert.match(css, /grid-template-columns:repeat\(5,minmax\(0,1fr\)\)/);
  assert.match(app, /quickLimit:60/);
  assert.match(app, /loadQuickChannels\(append=false\)/);
  assert.doesNotMatch(html, /id="quick-load-more"|class="quick-all"/);
  assert.match(css, /scrollbar-width:none/);
  assert.doesNotMatch(app, /quickLimit(?::|=)12/);
  assert.match(html, /class="quick-catalog-scroll" tabindex="0" role="region" aria-label="Catálogo de canales"/);
  assert.match(workspace, /getBoundingClientRect\(\)\.top \+ window.scrollY/);
  assert.match(workspace, /new ResizeObserver\(fitOverviewViewport\)/);
});

test('lazy catalog appends offset pages, avoids duplicates and stops on an empty page', async () => {
  const source=app.match(/^async function loadQuickChannels\(append=false\)\{[\s\S]*?^\}/m)?.[0];
  assert.ok(source);
  const calls=[];const pages=[[{id:1},{id:2}],[{id:2},{id:3}],[]];
  const scope={api:'/api/v1',state:{quickRequest:0,quickChannels:[],quickTotal:0,quickLimit:60,quickMode:'all',quickQuery:''},renderQuickChannels(){},document:{dispatchEvent(){}},Event:class{},fetch:async url=>{calls.push(url);return {ok:true,headers:{get:()=> '100'},json:async()=>pages.shift()}}};
  vm.createContext(scope);vm.runInContext(source,scope);
  await scope.loadQuickChannels();assert.match(calls[0],/limit=60&offset=0/);
  await scope.loadQuickChannels(true);assert.match(calls[1],/offset=2/);
  assert.deepEqual(Array.from(scope.state.quickChannels,c=>c.id),[1,2,3]);
  await scope.loadQuickChannels(true);assert.equal(scope.state.quickTotal,3);
  await scope.loadQuickChannels(true);assert.equal(calls.length,3);
  assert.equal(scope.state.quickLoading,false);
});

test('lazy catalog retains loaded channels on failure and blocks overlapping/filter-pending loads', async () => {
  const source=app.match(/^async function loadQuickChannels\(append=false\)\{[\s\S]*?^\}/m)[0];
  let calls=0;
  const scope={api:'/api/v1',state:{quickRequest:0,quickChannels:[{id:1}],quickTotal:100,quickLimit:60,quickMode:'all',quickQuery:'',quickLoading:true},renderQuickChannels(){},document:{dispatchEvent(){}},Event:class{},fetch:async()=>{calls++;throw new Error('offline')}};
  vm.createContext(scope);vm.runInContext(source,scope);
  await scope.loadQuickChannels(true);assert.equal(calls,0);
  scope.state.quickLoading=false;scope.state.quickFilterPending=true;
  await scope.loadQuickChannels(true);assert.equal(calls,0);
  scope.state.quickFilterPending=false;
  await assert.rejects(scope.loadQuickChannels(true),/offline/);
  assert.equal(scope.state.quickChannels.length,1);assert.equal(scope.state.quickLoadError,true);assert.equal(scope.state.quickLoading,false);
});

test('source sync labels separate work in progress, idle and real errors', () => {
  const source = ['sourceSyncRunning','sourceSyncLabel'].map(name=>app.match(new RegExp(`^function ${name}[^\\n]+`,'m'))?.[0]).join('\n');
  const scope={};vm.createContext(scope);vm.runInContext(source,scope);
  assert.equal(scope.sourceSyncLabel({id:1,last_status:'error'},[{source_id:1,status:'running'}]),'● Sincronizando…');
  assert.equal(scope.sourceSyncLabel({id:1,last_status:'idle'},[]),'● Sin emisiones disponibles');
  assert.equal(scope.sourceSyncLabel({id:1,last_status:'error'},[]),'● Error');
  assert.equal(scope.sourceSyncRunning(1,[{source_id:2,status:'running'}]),false);
});

test('sync completion automatically refreshes channels, overview and source state', async () => {
  const source=app.match(/^async function refreshSourceSync\(\)\{[\s\S]*?^\}/m)?.[0];
  assert.ok(source);
  let jobs=[{id:1,status:'running'}];const calls=[];
  const scope={state:{},document:{hidden:false},api:'/api/v1',console,request:async()=>jobs};
  for(const name of ['loadSources','loadChannels','loadQuickChannels','loadDashboard'])scope[name]=async()=>{calls.push(name)};
  vm.createContext(scope);vm.runInContext(source,scope);
  await scope.refreshSourceSync();assert.equal(calls.length,4);
  await scope.refreshSourceSync();assert.equal(calls.length,4,'unchanged state must not reload the UI');
  jobs=[{id:1,status:'completed'}];await scope.refreshSourceSync();assert.equal(calls.length,8);
  assert.equal(scope.state.syncPolling,false);
  scope.document.hidden=true;jobs=[{id:2,status:'running'}];await scope.refreshSourceSync();assert.equal(calls.length,8);
  scope.document.hidden=false;scope.state.syncPolling=true;await scope.refreshSourceSync();assert.equal(calls.length,8);
});

test('broadcast state distinguishes a replay from a live channel without marking an error', () => {
  const source = app.match(/^function broadcastLabel[^\n]+/m)?.[0];
  assert.ok(source);
  const scope = {};
  vm.createContext(scope);
  vm.runInContext(source, scope);
  assert.equal(scope.broadcastLabel({broadcast_state:'live'}, 'Disponible'), 'En directo');
  assert.equal(scope.broadcastLabel({broadcast_state:'replay'}, 'Disponible'), 'Repetición disponible');
  assert.equal(scope.broadcastLabel({}, 'Disponible'), 'Disponible');
  assert.match(app, /broadcastLabel\(c,'Disponible'\)/);
  assert.match(app, /broadcastLabel\(channel,channel.group/);
});

test('escaped provider data is safe in text and quoted HTML attributes', () => {
  const source = app.match(/^function escapeHTML[^\n]+/m)?.[0];
  assert.ok(source, 'escapeHTML must remain testable');
  const scope = {
    document: {createElement: () => ({
      textContent: '',
      get innerHTML() {
        return String(this.textContent).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
      },
    })},
  };
  vm.createContext(scope);
  vm.runInContext(source, scope);
  assert.equal(scope.escapeHTML(`Canal " onclick="alert('x') <script>&`), 'Canal &quot; onclick=&quot;alert(&#39;x&#39;) &lt;script&gt;&amp;');
  assert.equal(scope.escapeHTML('La 1 · España'), 'La 1 · España');
});

test('every locally referenced interface asset exists', () => {
  const page = fs.readFileSync(path.join(web, 'index.html'), 'utf8');
  for (const match of page.matchAll(/(?:src|href)="(\/(?:icons|brand|vendor)\/[^"?]+|\/[^/"?]+\.(?:css|js))"/g)) {
    assert.ok(fs.existsSync(path.join(web, match[1])), `Missing ${match[1]}`);
  }
});

test('new workspace scripts parse without dependencies', () => {
  for (const name of ['app.js', 'management-ui.js', 'workspace.js']) {
    assert.doesNotThrow(() => new vm.Script(fs.readFileSync(path.join(web, name), 'utf8'), {filename:name}));
  }
});

test('search focus is drawn around the rounded shell, not the inner input', () => {
  const css = fs.readFileSync(path.join(web, 'workspace.css'), 'utf8');
  assert.match(css, /\.quick-search:focus-within,\.search:focus-within\{[^}]*border-radius:999px;[^}]*outline:2px/);
  assert.match(css, /\.quick-search input:focus-visible,\.search input:focus-visible\{outline:none;box-shadow:none\}/);
});

test('inactive source modes disable their invalid inputs', () => {
  const fields = Object.fromEntries(['url','file','manual','redbull'].map(key=>[key,{classList:{toggle(){}},inputs:[{disabled:false}]}]));
  const nodes = new Map();
  const $ = selector => {
    const match=selector.match(/^#source-(url|file|manual|redbull)-fields$/);
    if(match)return {...fields[match[1]],querySelectorAll:()=>fields[match[1]].inputs};
    if(!nodes.has(selector))nodes.set(selector,{value:'',checked:false});
    return nodes.get(selector);
  };
  const source=app.slice(app.indexOf('function setSourceMode('),app.indexOf('\nfunction openSourceDialog('));
  const scope={state:{},$, $$:()=>[]};vm.createContext(scope);vm.runInContext(source,scope);
  scope.setSourceMode('url');scope.setSourceMode('manual');
  assert.equal(fields.url.inputs[0].disabled,true);
  assert.equal(fields.manual.inputs[0].disabled,false);
  assert.equal($('#manual-ace-id').required,true);
  assert.equal($('#source-url').required,false);
  scope.setSourceMode('file');
  assert.equal(fields.manual.inputs[0].disabled,true);
  assert.equal($('#source-file').required,true);
});

test('latest filter request wins even if earlier responses arrive later', async () => {
  const text=fs.readFileSync(path.join(web,'management-ui.js'),'utf8');
  const fn=text.slice(text.indexOf('async function managedLoadChannels('),text.indexOf('\nfunction managedRenderChannels('));
  const pending=[],renders=[];
  const scope={AbortController,channelUI:{requestID:0,page:0},state:{channels:[]},$(){return {classList:{add(){},remove(){}}}},toast(){},managedChannelRequest(){return new Promise(resolve=>pending.push(resolve))},managedRenderChannels(){renders.push(scope.state.channels[0].name)}};
  vm.createContext(scope);vm.runInContext(fn,scope);
  const first=scope.managedLoadChannels(),second=scope.managedLoadChannels();
  pending[1]({channels:[{name:'new'}],total:1});await second;
  pending[0]({channels:[{name:'old'}],total:30});await first;
  assert.deepEqual(renders,['new']);assert.equal(scope.channelUI.total,1);assert.equal(scope.channelUI.loading,false);
});

test('Escape cleans video, HLS and viewer heartbeat', () => {
  const listeners={};let paused=false,destroyed=false,released=false,cleared=false;
  const video={pause(){paused=true},removeAttribute(){},load(){},hasAttribute(){return false}};
  const dialog={open:true,close(){this.open=false},addEventListener(type,fn){listeners[type]=fn}};
  const scope={state:{playerToken:0,playerSession:{channelID:1,viewerID:'viewer'},playerHeartbeat:42,playerHLS:{destroy(){destroyed=true}}},$:s=>s==='#channel-player'?video:dialog,clearInterval(){cleared=true},fetch(){released=true;return Promise.resolve()},api:'/api/v1'};
  vm.createContext(scope);
  vm.runInContext(app.match(/^function releasePlayerSession[^\n]+/m)[0]+'\n'+app.match(/^function closePlayer[^\n]+/m)[0],scope);
  vm.runInContext(app.match(/^\$\('#player-dialog'\)\.addEventListener\('cancel'[^\n]+/m)[0],scope);
  let prevented=false;listeners.cancel({preventDefault(){prevented=true}});
  assert.ok(prevented&&paused&&destroyed&&released&&cleared);
  assert.equal(dialog.open,false);assert.equal(scope.state.playerSession,null);assert.equal(scope.state.playerHLS,null);
});

test('failed preparation never falls back to a private Ace Engine URL', async () => {
  const nodes=new Map();const $=selector=>{if(!nodes.has(selector))nodes.set(selector,{open:false,showModal(){this.open=true}});return nodes.get(selector)};
  const fn=app.slice(app.indexOf('async function openPlayer('),app.indexOf("\n$('#menu-button')"));
  const scope={state:{channels:[{id:1,name:'Ace',stream_type:'acestream'}],settings:{engine_url:'http://192.0.2.10:6878'},playerToken:0},$,api:'/api/v1',closePlayer(){scope.state.playerToken++},request:async()=>{throw Error('engine unavailable')}};
  vm.createContext(scope);vm.runInContext(fn,scope);await scope.openPlayer(1);
  assert.equal($('#open-stream').href,'/api/v1/channels/1/stream');
  assert.match($('#player-status').textContent,/No se pudo preparar/);
  assert.equal($('#channel-player').src,undefined);
});

test('unavailable clipboard gives a selectable URL and visible feedback', async () => {
  const source=app.slice(app.indexOf('async function copyField('),app.indexOf('\nfunction closeMobileMenu'));
  let focused=false,selected=false,message='';
  const scope={$:()=>({value:'https://tv.example/playlist.m3u',focus(){focused=true},select(){selected=true}}),navigator:{clipboard:{writeText:async()=>{throw Error('denied')}}},toast(text){message=text}};
  vm.createContext(scope);vm.runInContext(source,scope);await scope.copyField('#playlist-url');
  assert.ok(focused&&selected);assert.match(message,/manualmente/);
});

test('an EPG failure does not hide successful stream activity', async () => {
  const text=fs.readFileSync(path.join(web,'workspace.js'),'utf8');
  const fn=text.slice(text.indexOf('  async function refreshOverview('),text.indexOf("\n  document.querySelector('#overview-copy-playlist')"));
  const nodes=new Map();
  const element=()=>({textContent:'',value:'',classList:{contains:()=>true},replaceChildren(){},append(){}});
  const document={hidden:false,createElement:element,querySelector(selector){if(!nodes.has(selector))nodes.set(selector,element());return nodes.get(selector)}};
  const scope={document,state:{settings:null},location:{origin:'https://tv.example'},api:'/api/v1',request:async path=>{if(path.endsWith('/epg'))throw Error('unavailable');return []}};
  vm.createContext(scope);vm.runInContext('let refreshing=false;\n'+fn,scope);
  await scope.refreshOverview();
  assert.equal(document.querySelector('#overview-stream-count').textContent,0);
  assert.match(document.querySelector('#overview-epg-status').textContent,/No se pudo/);
});

test('closing during preparation releases the late viewer without starting media', async () => {
  const nodes=new Map();const $=selector=>{if(!nodes.has(selector))nodes.set(selector,{open:false,showModal(){this.open=true}});return nodes.get(selector)};
  let resolve,deleted='';
  const fn=app.slice(app.indexOf('async function openPlayer('),app.indexOf("\n$('#menu-button')"));
  const scope={state:{channels:[{id:1,name:'IPTV',stream_type:'hls'}],playerToken:0},$,api:'/api/v1',closePlayer(){scope.state.playerToken++},request:()=>new Promise(r=>{resolve=r}),fetch:async url=>{deleted=url}};
  vm.createContext(scope);vm.runInContext(fn,scope);
  const loading=scope.openPlayer(1);scope.state.playerToken++;$('#player-dialog').open=false;
  resolve({viewer_id:'late-viewer',proxy_url:'/proxy'});await loading;
  assert.equal(deleted,'/api/v1/channels/1/viewers/late-viewer');assert.equal($('#channel-player').src,undefined);
});
