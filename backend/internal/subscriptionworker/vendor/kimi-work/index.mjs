import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {createHash} from 'node:crypto';

const ID = 'kimi-work';
const BASE = 'https://agent-gw.kimi.com/coding/v1';
const MEMBER = 'https://www.kimi.com/apiv2/kimi.gateway.membership.v2.MembershipService/';
// Kimi's desktop builds its Work model menu from this call, not from
// agent-gw's /models: models plus moreModels, each with the id Work's
// gateway takes (requestParams.modelId). An account's tier models, K3.1 Pro
// on the 99 plan among them, are listed here and not in /models.
const CONFIG = 'https://www.kimi.com/apiv2/kimi.gateway.config.v1.ConfigService/DescribeKimiWorkConfig';
// What Kimi's own Work client sends agent-gw (Kimi 3.2.20's daimon 0.6.10,
// createDaimonKimiCodeRequestHeaders): the vendored Kimi Code's version as
// "kimi-code-cli/<version>" (no daimon caller passes a suffix), and the
// X-Msh device headers beside it (magpie-community/plugins#353).
const KIMI_CODE_VERSION = '0.36.0';
const DAIMON_VERSION = '0.6.10';
const UA = 'kimi-code-cli/' + KIMI_CODE_VERSION;
// What Kimi's desktop sends www.kimi.com/apiv2 besides the sign-in (its
// Connect interceptor: x-msh-platform "mac" or "windows" on the desktop,
// "web" otherwise, x-msh-version the desktop's version, R-Timezone). They
// are not decoration: asked without them DescribeKimiWorkConfig answers
// with the web menu (k3-agent, k3-agent-swarm, k2d8-preview), and asked as
// the desktop asks with its own (k3d1-agent, K3.1 Pro, first), so through
// 0.1.8 K3.1 Pro was never listed (tests/fixtures/work-config.*.json).
const DESKTOP_VERSION = '3.2.20';
function webHeaders(platform=process.platform) {
  return {'User-Agent':UA,'x-msh-platform':platform==='darwin'?'mac':platform==='win32'?'windows':'web',
    'x-msh-version':DESKTOP_VERSION,'R-Timezone':Intl.DateTimeFormat().resolvedOptions().timeZone||'UTC'};
}
const ascii = (v, fallback = 'unknown') => String(v ?? '').replace(/[^\x20-\x7e]/g, '').trim() || fallback;
function deviceModel() {
  const t = os.type(), r = os.release(), a = os.arch();
  if (t === 'Darwin') return 'macOS ' + r + ' ' + a;
  if (t === 'Windows_NT') return 'Windows ' + r + ' ' + a;
  return (t + ' ' + r + ' ' + a).trim();
}
function workHeaders(deviceId) {
  return {'User-Agent':UA,...(deviceId?{'X-Msh-Device-Id':deviceId}:{}),
    'X-Msh-Device-Model':ascii(deviceModel()),'X-Msh-Device-Name':ascii(os.hostname()),'X-Msh-Os-Version':ascii(os.release()),
    'X-Msh-Platform':'kimi-code-cli','X-Msh-Version':KIMI_CODE_VERSION,'X-Msh-Daimon-Version':DAIMON_VERSION};
}
const expired = () => Object.assign(Error('请先在本机 Kimi 桌面版登录 Work，再重新连接 Kimi Work 供应商。'), {signIn:'expired',code:'kimi_work_login_expired'});
const unavailable = error => Object.assign(Error('Kimi Work 连接失败：' + (error?.cause?.code || error?.message || '未知网络错误'), {cause:error}), {code:'kimi_work_connection_failed'});
const percent = x => typeof x === 'number' && Number.isFinite(x) ? Math.max(0,Math.min(100,x*100)) : undefined;

// Kimi Work validates tools against Moonshot's "moonshot flavored json
// schema" and refuses the whole request, 400 "tools.function.parameters is
// not a valid moonshot flavored json schema", for shapes a JSON Schema
// allows and agents send (#287). Codex's MCP and app tools are zod's
// toJSONSchema with reused schemas as $defs (__schema0, __schema1 …), and
// zod writes a schema derived from a reused one as a $ref beside what it
// changed: z.number() reused, then .int(), is {"type":"integer","maximum":…,
// "$ref":"#/$defs/__schema0"}, refused "when using $ref, type should be
// defined in the referenced schema instead of the parent schema". The tools
// are rewritten for the rules Kimi has been seen to hold:
// - a $ref beside a type: the target, when it has no $ref of its own, is
//   written in its place with the parent's keywords over it (what zod's
//   derived schema means); otherwise the parent's type is left out;
// - a type beside an anyOf ("type should be defined in anyOf items instead
//   of the parent schema") is left out;
// - an enum or const with no type gets the one its values have, or it and
//   "null" ("type is not defined");
// - a $ref's target with no type gets the one its keywords or branches say
//   ("detected infinite recursion without termination condition").
// A body Kimi takes as it is goes byte for byte; a rewrite is the same for
// the same tools, so a cached prefix stays one.
const isObj=x=>x!==null&&typeof x==='object'&&!Array.isArray(x);
const MAP_KEYS=['properties','$defs','definitions','dependentSchemas','patternProperties'];
const SUB_KEYS=['items','additionalProperties','contains','propertyNames','not','if','then','else','allOf','anyOf','oneOf','prefixItems','unevaluatedProperties','unevaluatedItems','contentSchema'];
const NOTES=new Set(['$ref','description','title','default','examples','$comment','deprecated','readOnly','writeOnly']);
const ALL_TYPES=['string','number','integer','boolean','object','array','null'];

function eachSchema(node,f) {
  if(Array.isArray(node)) {for(const v of node)eachSchema(v,f);return;}
  if(!isObj(node))return;
  f(node);
  for(const k of MAP_KEYS) if(isObj(node[k])) for(const v of Object.values(node[k])) eachSchema(v,f);
  for(const k of SUB_KEYS) if(k in node) eachSchema(node[k],f);
}

function refTarget(root,ref) {
  if(typeof ref!=='string'||(ref!=='#'&&!ref.startsWith('#/')))return undefined;
  let cur=root;
  for(let tok of ref.split('/').slice(1)) {
    tok=tok.replace(/~1/g,'/').replace(/~0/g,'~');
    try{tok=decodeURIComponent(tok);}catch{}
    if(!isObj(cur)&&!Array.isArray(cur))return undefined;
    if(!Object.hasOwn(cur,tok))return undefined;
    cur=cur[tok];
  }
  return cur;
}

const hasRefInside=s=>JSON.stringify(s).includes('"$ref"');

function valueType(v) {
  if(v===null)return 'null';
  switch(typeof v){
    case 'string':return 'string';
    case 'boolean':return 'boolean';
    case 'number':return Number.isInteger(v)?'integer':'number';
  }
  return Array.isArray(v)?'array':'object';
}

// the type an enum of vals is given: the one all non-null values have, with
// "null" when a null is among them; undefined when there are none or they
// are of more than one kind
function enumType(vals) {
  let t,nul=false;
  for(const v of vals) {
    const vt=valueType(v);
    if(vt==='null'){nul=true;continue;}
    if(vt==='array'||vt==='object')return undefined;
    if(t===undefined||t===vt)t=vt;
    else if((t==='integer'&&vt==='number')||(t==='number'&&vt==='integer'))t='number';
    else return undefined;
  }
  if(t===undefined)return undefined;
  return nul?[t,'null']:t;
}

// the types s can have, as its type, keywords, values or branches say;
// undefined when they don't say
function schemaTypes(root,s,depth=0) {
  if(depth>16||!isObj(s))return undefined;
  if('type' in s)return new Set([].concat(s.type).map(String));
  if('$ref' in s){const t=refTarget(root,s.$ref);return t===undefined?undefined:schemaTypes(root,t,depth+1);}
  if(Array.isArray(s.enum)||'const' in s)return new Set(('const' in s?[s.const]:s.enum).map(valueType));
  for(const k of ['oneOf','anyOf','allOf']) {
    if(!Array.isArray(s[k]))continue;
    const ts=new Set();
    for(const b of s[k]) {
      const bt=schemaTypes(root,b,depth+1);
      if(!bt){if(k!=='allOf')return undefined;continue;}
      for(const t of bt)ts.add(t);
    }
    if(ts.size)return ts;
  }
  const has=(...ks)=>ks.some(k=>k in s);
  if(has('properties','required','additionalProperties','patternProperties','minProperties','maxProperties','propertyNames','dependentRequired','dependentSchemas','unevaluatedProperties'))return new Set(['object']);
  if(has('items','prefixItems','minItems','maxItems','uniqueItems','contains','unevaluatedItems'))return new Set(['array']);
  if(has('minLength','maxLength','pattern','format','contentEncoding','contentMediaType'))return new Set(['string']);
  if(has('minimum','maximum','exclusiveMinimum','exclusiveMaximum','multipleOf'))return new Set(['number']);
  return undefined;
}

function typeFor(root,s) {
  if(Array.isArray(s.enum))return enumType(s.enum);
  if('const' in s)return enumType([s.const]);
  const ts=schemaTypes(root,s);
  if(!ts||!ts.size)return [...ALL_TYPES];
  if(ts.has('integer')&&ts.has('number'))ts.delete('integer');
  const names=ALL_TYPES.filter(t=>ts.has(t));
  return names.length===1?names[0]:names;
}

// the same object with type written first, as an agent would have
const withType=(s,type)=>{const rest={...s};delete rest.type;for(const k of Object.keys(s))delete s[k];Object.assign(s,{type},rest);};

function moonshotSchema(root) {
  let changed=false;
  // a $ref beside a type, and a type beside an anyOf
  eachSchema(root,s=>{
    if('$ref' in s&&'type' in s) {
      const t=refTarget(root,s.$ref);
      if(isObj(t)&&!hasRefInside(t)) {
        const own={...s};delete own.$ref;
        for(const k of Object.keys(s))delete s[k];
        Object.assign(s,structuredClone(t),own);
      } else delete s.type;
      changed=true;
    }
    if(Array.isArray(s.anyOf)&&'type' in s){delete s.type;changed=true;}
  });
  // an enum or const with no type
  eachSchema(root,s=>{
    if('type' in s||!(Array.isArray(s.enum)||'const' in s))return;
    const t=enumType(Array.isArray(s.enum)?s.enum:[s.const]);
    if(t!==undefined){withType(s,t);changed=true;}
  });
  // a $ref's target with no type
  eachSchema(root,s=>{
    if(typeof s.$ref!=='string')return;
    const t=refTarget(root,s.$ref);
    if(!isObj(t)||'type' in t||'anyOf' in t||'$ref' in t||!Object.keys(t).length)return;
    const typ=typeFor(root,t);
    if(typ!==undefined){withType(t,typ);changed=true;}
  });
  return changed;
}

// body with each tool's parameters as Kimi takes them; the same string when
// nothing had to change or it isn't a chat body with tools
function moonshotTools(body) {
  if(typeof body!=='string'||!body.includes('"tools"'))return body;
  let doc;
  try{doc=JSON.parse(body);}catch{return body;}
  if(!isObj(doc)||!Array.isArray(doc.tools))return body;
  let changed=false;
  for(const tool of doc.tools) {
    const p=tool?.function?.parameters;
    if(isObj(p)&&moonshotSchema(p))changed=true;
  }
  return changed?JSON.stringify(doc):body;
}

function desktopFile({home=os.homedir(), platform=process.platform, appdata=process.env.APPDATA}={}) {
  const parent=platform==='win32' ? appdata || path.join(home,'AppData','Roaming')
    : platform==='darwin' ? path.join(home,'Library','Application Support') : path.join(home,'.config');
  return path.join(parent,'kimi-desktop','daimon-share','daimon','config.json');
}

// The install id Kimi's daimon made for its Kimi Code home
// (<daimon>/runtime/kimi-code/home/device_id, a randomUUID it writes once)
// and sends as X-Msh-Device-Id. It is read, never made or written here: the
// desktop app owns it, and without it the header is left out.
function readDeviceId(file) {
  try {
    const id=fs.readFileSync(path.join(path.dirname(file),'runtime','kimi-code','home','device_id'),'utf8').trim();
    return /^[\x21-\x7e]{1,128}$/.test(id)?id:undefined;
  } catch {return undefined;}
}

// Read the live Work sign-in, including after rotation. Never consume its
// refresh token or write the desktop app's configuration. The internal field
// is called kimiCode, but this credential is issued for agent-gw (Work).
function readDesktop(file=desktopFile()) {
  try {
    const st=fs.statSync(file);
    if(!st.isFile() || st.size>8*1024*1024) throw expired();
    const doc=JSON.parse(fs.readFileSync(file,'utf8').replace(/^\uFEFF/,''));
    const work=doc.credentials?.kimiCode, web=doc.credentials?.kimiWeb;
    if(typeof work?.apiKey!=='string'||!work.apiKey || typeof web?.accessToken!=='string'||!web.accessToken
      || typeof web.userId!=='string'||!web.userId || work.baseUrl?.replace(/\/$/,'')!==BASE) throw expired();
    return {workToken:work.apiKey,webToken:web.accessToken,deviceId:readDeviceId(file),
      uid:createHash('sha256').update('kimi-work-account\0'+web.userId).digest('hex')};
  } catch {throw expired();}
}

function modelRows(payload) {
  if(!Array.isArray(payload?.data)||payload.has_more||payload.data.length>100) throw Error('Kimi Work 模型目录暂不可用。');
  const out={};
  for(const m of payload.data) {
    if(!isObj(m)||typeof m.id!=='string'||!m.id||m.id.length>256||/[\x00-\x20/]/.test(m.id)) continue;
    // One model's effort list in a shape not seen before costs that model
    // its unreadable levels, not the account its whole catalog: a throw
    // here left only the fallback k3-agent listed. Only levels Kimi lists
    // are kept, so none is guessed.
    const variants=Object.create(null);
    for(const x of Array.isArray(m.reasoningEffortOptions)?m.reasoningEffortOptions:[]) {
      const effort=String(isObj(x)&&typeof x.effort==='string'?x.effort:'').replace(/^REASONING_EFFORT_/,'').toLowerCase();
      if(/^[a-z][a-z0-9_-]{0,63}$/.test(effort))variants[effort]={reasoningEffort:effort};
    }
    out[m.id]={id:m.id,name:m.display_name||m.id,
      api:{id:m.id,url:BASE,npm:'@ai-sdk/openai-compatible'},
      // The server lists the plan's maximum XL context; default to standard
      // context rather than silently asking for a more expensive XL session.
      limit:{context:Math.min(Number(m.context_length)||262144,262144),output:32768},
      capabilities:{reasoning:!!m.supports_reasoning,toolcall:true,attachment:!!m.supports_image_in,
        input:{text:true,image:!!m.supports_image_in,video:false,audio:false,pdf:false},output:{text:true}},
      variants};
  }
  if(!Object.keys(out).length)throw Error('Kimi Work 模型目录为空。');
  return out;
}

// DescribeKimiWorkConfig's reply, Connect JSON (camelCase, or the proto's
// own snake_case names, which the desktop asks for).
const pick=(o,a,b)=>isObj(o)?(o[a]??o[b]):undefined;
const EFFORTS=new Set(['off','low','medium','high','xhigh','max']);
function workConfigRows(payload) {
  const list=[...(Array.isArray(pick(payload,'models','models'))?pick(payload,'models','models'):[]),
    ...(Array.isArray(pick(payload,'moreModels','more_models'))?pick(payload,'moreModels','more_models'):[])];
  if(list.length>100)throw Error('Kimi Work 模型目录暂不可用。');
  const out={};
  for(const m of list) {
    const rp=pick(m,'requestParams','request_params'),display=pick(m,'display','display');
    const dc=pick(m,'daimonModelConfig','daimon_model_config');
    const id=String(pick(rp,'modelId','model_id')??'').trim();
    if(!id||id.length>256||/[\x00-\x20/]/.test(id)||out[id])continue;
    const variants=Object.create(null);
    // The levels requestParams lists, else the daimon's supportEfforts, as
    // the desktop does; "off" is REASONING_EFFORT_NONE there.
    const opts=pick(rp,'reasoningEffortOptions','reasoning_effort_options');
    for(const x of Array.isArray(opts)?opts:[]) {
      const effort=String(pick(x,'reasoningEffort','reasoning_effort')||pick(x,'effort','effort')||'').trim().replace(/^REASONING_EFFORT_/,'').toLowerCase();
      if(/^[a-z][a-z0-9_-]{0,63}$/.test(effort)&&effort!=='unspecified')variants[effort]={reasoningEffort:effort};
    }
    const support=pick(dc,'supportEfforts','support_efforts');
    if(!Object.keys(variants).length)for(const x of Array.isArray(support)?support:[]) {
      const effort=String(x).trim().toLowerCase();
      if(EFFORTS.has(effort))variants[effort==='off'?'none':effort]={reasoningEffort:effort==='off'?'none':effort};
    }
    const caps=pick(dc,'capabilities','capabilities');
    const has=c=>Array.isArray(caps)&&caps.includes(c);
    const image=has('image_in'),thinking=has('thinking')||Object.keys(variants).length>0;
    const max=Number(pick(dc,'maxContextSize','max_context_size'))||262144;
    const output=Number(pick(rp,'maxTokens','max_tokens'))||32768;
    out[id]={id,name:String(pick(display,'displayName','display_name')||'').trim()||id,
      api:{id,url:BASE,npm:'@ai-sdk/openai-compatible'},
      // Standard context, as /models' rows: XL is a header the desktop
      // sends only when the user picks it.
      limit:{context:Math.min(max,262144),output},
      capabilities:{reasoning:thinking,toolcall:true,attachment:image,
        input:{text:true,image,video:false,audio:false,pdf:false},output:{text:true}},
      variants};
  }
  return out;
}

function createPlugin({readSession=readDesktop,fetchImpl=globalThis.fetch,platform=process.platform}={}) {
  const current=auth=>{
    const session=readSession();
    if(auth?.source!=='kimi-work-desktop'||auth.uid!==session.uid)throw expired();
    return session;
  };
  async function jsonRequest(url,token,body,signal,deviceId) {
    const response=await fetchImpl(url,{method:body===undefined?'GET':'POST',
      headers:{...(token?{Authorization:'Bearer '+token}:{}),...(url.startsWith(BASE)?workHeaders(deviceId):webHeaders(platform)),'Accept':'application/json',
        ...(body===undefined?{}:{'Content-Type':'application/json','Connect-Protocol-Version':'1'})},
      ...(body===undefined?{}:{body:JSON.stringify(body)}),signal:signal||AbortSignal.timeout(15000),redirect:'error'});
    if(response.status===401)throw Object.assign(expired(),{status:401});
    if(!response.ok)throw Object.assign(Error('Kimi Work 服务暂不可用（HTTP '+response.status+'）。'),{status:response.status});
    if(Number(response.headers.get('content-length')||0)>2*1024*1024)throw Error('Kimi Work 响应过大。');
    const text=await response.text();
    if(Buffer.byteLength(text)>2*1024*1024)throw Error('Kimi Work 响应过大。');
    try{return JSON.parse(text);}catch{throw Error('Kimi Work 响应格式无效。');}
  }
  // Work's menu, as the desktop asks it with its web sign-in. When that
  // ask fails (the web sign-in in Kimi's file has gone stale, which
  // www.kimi.com answers 401 "invalid user token"; any other status; a
  // reply with no models), the menu is asked again with no sign-in, as the
  // desktop asks it before one: www.kimi.com answers that with the same
  // desktop menu (tests/fixtures/work-config.mac.json, K3.1 Pro first).
  // Through 0.1.9 a failed ask was dropped without a word, so the list was
  // /models' alone (k3-agent, k2d8-preview) and K3.1 Pro was missing with
  // nothing in the log. Each failure is logged now, without the token.
  async function menu(s) {
    let why='no web sign-in';
    if(s.webToken) {
      try {
        const rows=workConfigRows(await jsonRequest(CONFIG,s.webToken,{}));
        if(Object.keys(rows).length)return rows;
        why='a reply with no models';
      } catch(error) {why=error?.status?'HTTP '+error.status:(error?.name==='TimeoutError'?'timed out':error?.cause?.code||error?.message||String(error));}
    }
    console.warn('kimi-work: DescribeKimiWorkConfig with the web sign-in failed ('+why+'); asking it without one');
    try {return workConfigRows(await jsonRequest(CONFIG,'',{}));}
    catch(error) {
      console.warn('kimi-work: DescribeKimiWorkConfig without a sign-in failed ('+(error?.status?'HTTP '+error.status:error?.message||String(error))+'); listing /models alone');
      throw error;
    }
  }
  return {
    config:async cfg=>{
      cfg.provider??={};
      cfg.provider[ID]??={name:'Kimi Work',npm:'@ai-sdk/openai-compatible',api:BASE,
        // Shown until the account's own list is read, and kept when it can't
        // be: the two ids Work's /models has listed (tests/fixtures).
        models:{'k3-agent':{name:'K3 · Work',limit:{context:262144,output:32768},tool_call:true,reasoning:true},
          'k2d8-preview':{name:'K2.8 Preview · Work',limit:{context:262144,output:32768},tool_call:true,reasoning:true}}};
    },
    auth:{
      provider:ID,
      maxConcurrency:1,
      methods:[{type:'oauth',label:'使用本机 Kimi Work 登录',authorize:async()=>({
        url:'',method:'auto',instructions:'读取本机 Kimi 桌面版的 Work 登录；不会使用 Kimi Code API Key。',
        callback:async()=>{
          try{
            const s=readSession();await jsonRequest(BASE+'/models',s.workToken,undefined,undefined,s.deviceId);
            // Only an account reference is saved in Magpie. Actual credentials
            // stay in Kimi's own file and are reread for every operation.
            return {type:'success',access:'',refresh:'',expires:0,source:'kimi-work-desktop',
              accountId:'Kimi Work (local)',uid:s.uid};
          }catch{return {type:'failed',error:'没有可用的本机 Kimi Work 登录，请先打开 Kimi 并登录 Work。'};}
        },
      })}],
      loader:async getAuth=>({
        baseURL:BASE,apiKey:'local-desktop-session',
        async fetch(input,init={}) {
          const request=input instanceof Request?new Request(input,init):null;
          const url=new URL(request?request.url:String(input));
          if(url.origin!==new URL(BASE).origin||url.pathname!=='/coding/v1/chat/completions'
            ||url.username||url.password||url.search||url.hash||(request?.method||init.method||'POST')!=='POST') {
            throw Error('Kimi Work 仅接受官方 Work 网关的模型请求。');
          }
          const s=current(await getAuth());
          const headers=new Headers(request?request.headers:init.headers);
          for(const key of ['authorization','x-api-key','cookie','host','content-length','origin'])headers.delete(key);
          // magpie's own x-magpie-* hints are for the plugin, not for Kimi.
          for(const key of [...headers.keys()])if(key.startsWith('x-magpie-'))headers.delete(key);
          headers.set('Authorization','Bearer '+s.workToken);
          for(const [k,v] of Object.entries(workHeaders(s.deviceId)))headers.set(k,v);
          headers.set('Content-Type','application/json');
          // Preserve messages, stream and abort signal, and tools but for
          // the schema shapes Kimi refuses (moonshotTools). There is no
          // fallback to api.kimi.com, a Code API key, or another provider.
          const raw=request?request.body:init.body;
          let body=raw;
          if(raw!=null&&typeof raw!=='string')body=await new Response(raw).text();
          if(body!=null)body=moonshotTools(body);
          const options={...init,body,signal:request?request.signal:init.signal};
          if(body==null)delete options.body;
          let response;
          try { response=await fetchImpl(BASE+'/chat/completions',{...options,method:'POST',headers,redirect:'error'}); }
          catch(error) { if(options.signal?.aborted)throw error; throw unavailable(error); }
          if(response.status===401) { await response.body?.cancel(); throw expired(); }
          // Magpie classifies rate vs quota from the upstream response. Keep
          // its wording, reset details and Retry-After instead of guessing.
          return response;
        },
      }),
      // The allowance is read with Kimi's web sign-in (kimiWeb), not the
      // Work one: the desktop rewrites it in its file only while it runs, so
      // a closed desktop leaves a token www.kimi.com answers 401 "Session
      // expired". Through 0.1.10 every failure was thrown, which magpie
      // shows as a bare "暂时无法获取额度" with the reason only in a tooltip,
      // and a 401 marked the Work account signed out though chat (Work's
      // own token) still worked. A failure is now the card's error, and
      // the Work sign-in is kept.
      usage:async getAuth=>{
        const s=current(await getAuth());
        const plan='Kimi Work · 会员共享额度';
        let j;
        try {j=await jsonRequest(MEMBER+'GetSubscriptionStats',s.webToken,{});}
        catch(error) {
          const why=error?.status===401?'Kimi 网页登录已过期，打开一次 Kimi 桌面版即可刷新额度（对话不受影响）'
            :error?.name==='TimeoutError'?'读取 Kimi 会员额度超时':String(error?.message||error);
          console.warn('kimi-work: GetSubscriptionStats failed ('+(error?.status?'HTTP '+error.status:why)+')');
          return {plan,windows:[],error:why,signIn:'kept'};
        }
        // Connect JSON leaves a zero out: a balance with no ratio is 0% used.
        const b=pick(j,'subscriptionBalance','subscription_balance'),used=isObj(b)?percent(pick(b,'amountUsedRatio','amount_used_ratio')??0):undefined;
        if(used===undefined)return {plan,windows:[],error:'Kimi 会员额度回复里没有共享额度（subscriptionBalance）',signIn:'kept'};
        return {plan,windows:[{name:'会员共享额度',used}]};
      },
    },
    provider:{id:ID,models:async(provider,{auth}={})=>{
      if(auth?.source!=='kimi-work-desktop')return provider.models;
      const s=current(auth);
      // Both lists, the desktop's menu first. Either one failing leaves the
      // other; both failing throws, so the last good list is kept.
      const [cfgList,gw]=await Promise.allSettled([
        menu(s),
        jsonRequest(BASE+'/models',s.workToken,undefined,undefined,s.deviceId).then(modelRows)]);
      const a=cfgList.status==='fulfilled'?cfgList.value:{},b=gw.status==='fulfilled'?gw.value:{};
      if(gw.status==='rejected'&&!Object.keys(a).length)throw gw.reason;
      const out={...a};
      // An id both list keeps /models' row (the gateway's own context and
      // image flags) under the desktop's name for it.
      for(const [id,row] of Object.entries(b))out[id]=out[id]?{...row,name:out[id].name}:row;
      return out;
    }},
  };
}

export default async function KimiWorkProvider(){return createPlugin();}
export const _internal={createPlugin,moonshotTools,moonshotSchema,readDesktop,desktopFile,modelRows,workConfigRows,BASE,MEMBER,CONFIG,UA,workHeaders,webHeaders,readDeviceId};
