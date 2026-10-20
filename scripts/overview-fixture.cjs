// Isolated visual QA: node scripts/overview-fixture.cjs (no user DB or streams).
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const web = path.resolve(__dirname, '../internal/app/web');
const channels = Array.from({length:96}, (_,i)=>({id:i+1,source_id:1,source_name:'Fuente de prueba',source_kind:'file',name:`Canal de prueba ${String(i+1).padStart(2,'0')}`,group:'General',tvg_id:`test.${i+1}`,logo:'',acestream_id:'0'.repeat(40),enabled:true,stream_type:'acestream'}));
http.createServer((req,res)=>{
  const url = new URL(req.url, 'http://127.0.0.1');
  const data = {
    '/health':{}, '/api/v1/dashboard':{channels:96,enabled_channels:96,sources:1,groups:1,failed_sources:0,problem_channels:0},
    '/api/v1/sources':[{id:1,name:'Fuente de prueba',kind:'file',channel_count:96,last_status:'healthy'}],
    '/api/v1/settings':{engine_url:'',public_base_url:'',update_interval_hours:4,max_streams:4,stream_idle_seconds:60,history_retention_days:90},
    '/api/v1/epg':{enabled:false,cache_hours:6}, '/api/v1/streams':[], '/api/v1/sync-jobs':[], '/api/v1/history':[], '/api/v1/channel-groups':[{name:'General',count:96}],
  };
  if(req.method!=='GET'){res.writeHead(405);res.end();return;}
  if(url.pathname==='/api/v1/channels'){
    const filtered=channels.filter(c=>c.name.toLowerCase().includes((url.searchParams.get('q')||'').toLowerCase()));
    res.setHeader('X-Total-Count',filtered.length);
    const offset=Math.max(0,Number(url.searchParams.get('offset'))||0);
    res.setHeader('Content-Type','application/json');res.end(JSON.stringify(filtered.slice(offset,offset+(Number(url.searchParams.get('limit'))||96))));return;
  }
  if(Object.hasOwn(data,url.pathname)){res.setHeader('Content-Type','application/json');res.end(JSON.stringify(data[url.pathname]));return;}
  const file=path.resolve(web, '.'+(url.pathname==='/'?'/index.html':url.pathname));
  if(!file.startsWith(web+path.sep)||!fs.existsSync(file)||!fs.statSync(file).isFile()){res.writeHead(404);res.end();return;}
  const types={'.html':'text/html','.css':'text/css','.js':'text/javascript','.svg':'image/svg+xml'};
  res.setHeader('Content-Type',types[path.extname(file)]||'application/octet-stream');fs.createReadStream(file).pipe(res);
}).listen(18084,'127.0.0.1',()=>console.log('Isolated overview fixture: http://127.0.0.1:18084'));
