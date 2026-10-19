// Dependency-free frontend regressions: node --test scripts/workspace.test.cjs
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const web = path.join(__dirname, '../internal/app/web');
const app = fs.readFileSync(path.join(web, 'app.js'), 'utf8');

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
