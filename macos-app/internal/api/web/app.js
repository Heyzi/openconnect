let csrf = '', selected = '', currentStatus = {state:'loading'}, logEntries = [], lastDiagnosticSession = '', diagnosticRunning = false;
let refreshInFlight;
const $ = selector => document.querySelector(selector);
const escapeHTML = value => { const node = document.createElement('div'); node.textContent = value || ''; return node.innerHTML };
const escapeAttr = value => escapeHTML(value).replaceAll('"', '&quot;').replaceAll("'", '&#39;');
const duration = startedAt => { if(!startedAt)return '';const seconds=Math.max(0,Math.floor((Date.now()-new Date(startedAt).getTime())/1000));const h=String(Math.floor(seconds/3600)).padStart(2,'0'),m=String(Math.floor(seconds%3600/60)).padStart(2,'0'),s=String(seconds%60).padStart(2,'0');return `${h}:${m}:${s}` };
const formatBytes = value => { const units=['B','KB','MB','GB','TB'];let amount=Math.max(0,Number(value)||0),unit=0;while(amount>=1000&&unit<units.length-1){amount/=1000;unit++}return `${amount.toFixed(unit===0?0:1)} ${units[unit]}` };
function renderTraffic(traffic) {
  const chip = $('#traffic');
  chip.hidden = !traffic;
  if (!traffic) return;
  chip.innerHTML = `<span class="rate down">↓${formatBytes(traffic.downloadBytesPerSec)}/s</span><span class="rate up">↑${formatBytes(traffic.uploadBytesPerSec)}/s</span>`;
  chip.title = `${formatBytes(traffic.downloadBytes)} downloaded · ${formatBytes(traffic.uploadBytes)} uploaded`;
}
const parseRoutes = value => value.split(/[\s,]+/).map(x=>x.trim()).filter(Boolean);
async function req(path, options = {}) { options.headers = {...options.headers, 'X-CSRF-Token': csrf}; const response = await fetch(path, options); if (!response.ok) throw new Error(await response.text()); return response.status === 204 ? null : response.json() }
function showStatusError(message) { $('#lastError').textContent=message||'';$('#statusError').hidden=!message }
function refresh() { if(!refreshInFlight)refreshInFlight=refreshOnce().finally(()=>{refreshInFlight=null});return refreshInFlight }
async function refreshOnce() {
  const [status, response, inspection] = await Promise.all([req('/api/v1/status'), req('/api/v1/profiles'), req('/api/v1/inspection')]); const profiles = Array.isArray(response) ? response : [];
  currentStatus = status;
  if (!selected && profiles.length) { selected = profiles[0].id; await loadRoutes() }
  $('#openServerScripts').hidden = !(profiles.find(p => p.id === selected)?.saveServerScripts);
  $('#state').textContent = status.state; $('#state').className = 'status-pill ' + status.state; $('#state').title = status.lastError || ''; $('#statusMark').className = 'mark ' + status.state; renderTraffic(status.traffic);
  showStatusError(status.lastError);
  const busy = ['connecting','connected','disconnecting'].includes(status.state);
  $('#profiles').innerHTML = profiles.map(p => { const id=escapeAttr(p.id),active=p.id===status.profileId,backups=(p.fallbackServers||[]).length; const connected=active&&status.state==='connected'; const pending=active&&status.state==='connecting'; return `<div class="profile ${p.id===selected?'selected':''} ${connected?'active':''}" data-select="${id}" tabindex="0" role="group" aria-label="${escapeAttr(p.name)} VPN profile"><div class="profile-copy"><div class="profile-name-row"><b>${escapeHTML(p.name)}</b><span class="profile-protocol">${escapeHTML(p.protocol||'anyconnect')}</span></div><p>${escapeHTML(p.server)}${backups?` · +${backups} fallback`:''}</p></div><div class="profile-connection ${connected?'connected':pending?'pending':'idle'}"><i></i><span>${connected?`Connected · <span data-uptime>${duration(status.startedAt)}</span>`:pending?'Connecting…':'Ready'}</span></div><div class="profile-actions">${active&&busy?`<button class="ghost" data-disconnect>Disconnect</button>`:`<button data-connect="${id}" ${busy?'disabled':''}>Connect</button>`}<button class="ghost" data-edit="${id}" title="Edit profile">Edit</button><button class="delete-profile ghost danger" data-profile-delete="${id}" data-profile-name="${escapeAttr(p.name)}" ${active&&busy?'disabled':''} title="Delete profile">Delete</button></div></div>` }).join('') || '<p class="empty-state">No profiles yet. Add one to connect.</p>';
  renderInspection(inspection);
  if(status.state==='connected'&&status.startedAt&&lastDiagnosticSession!==status.startedAt)runDiagnostics(status.profileId,true);
}
const routeGroupCopy = source => ({
  'server-include':['Through VPN','Traffic to these networks is sent through the VPN.'],
  'server-exclude':['Bypass VPN','Traffic to these networks stays outside the VPN.'],
  'server':['VPN routes','Routes provided by the VPN server.'],
  'user':['Manual routes','Routes changed during this connection.'],
  'saved':['Saved routes','Routes saved in this profile for the next connection.']
}[source] || [source,'']);
async function loadRoutes() {
  const response = await req('/api/v1/routes'+(selected?'?profileId='+encodeURIComponent(selected):''));
  const routes = Array.isArray(response) ? response : [];
  const groups = routes.reduce((result,route) => {let group=result.find(item=>item.source===route.source);if(!group){group={source:route.source,routes:[]};result.push(group)}group.routes.push(route);return result},[]);
  $('#routes').innerHTML = groups.map(group => {const [name,description]=routeGroupCopy(group.source);return `<div class="route-group"><div class="route-group-title"><div><h3>${escapeHTML(name)}</h3><p>${escapeHTML(description)}</p></div><span>${group.routes.length}</span></div><div class="route-group-list">${group.routes.map(r => {const saved=r.source==='saved',overlap=r.overlaps?' overlap':'';return `<div class="route${overlap}"${r.overlaps?' title="This subnet overlaps another route"':''}><input value="${escapeHTML(r.cidr)}" ${saved?'readonly':`data-route-value="${r.id}"`}><div>${saved?'<span class="hint">Saved in profile</span>':`<button data-route-save="${r.id}">Save</button> <button class="ghost" data-route-delete="${r.id}">Delete</button>`}</div></div>`}).join('')}</div></div>`}).join('') || '<p class="hint">No routes received yet.</p>';
}
function renderInspection(report) {
  const stages=Array.isArray(report.stages)?report.stages:[];
  $('#inspection').innerHTML=stages.map(stage=>`<div class="stage ${escapeAttr(stage.status)}"><span class="stage-dot"></span><div><b>${escapeHTML(stage.name)}</b><p>${escapeHTML(stage.detail)}</p></div><span class="stage-status">${escapeHTML(stage.status)}</span></div>`).join('')||'<p class="hint">No connection events yet.</p>';
  const p=report.posture||{},meta=[];if(p.fileName)meta.push(p.fileName);if(p.size)meta.push(formatBytes(p.size));if(p.sha256)meta.push('SHA-256 '+p.sha256);
  $('#posture').innerHTML=`<div class="posture-card ${escapeAttr(p.status||'idle')}"><span class="stage-dot"></span><div><b>${escapeHTML(p.payloadReceived?'Payload captured':p.requested?'Posture requested':'No posture request')}</b><p>${escapeHTML(p.detail||'No posture information yet.')}</p>${meta.length?`<div class="posture-meta">${meta.map(escapeHTML).join('<br>')}</div>`:''}</div><span class="stage-status">${escapeHTML(p.status||'idle')}</span></div>`;
  $('#openPostureFiles').hidden=!p.payloadReceived;
}
function renderDiagnostics(report) {
  const checks=Array.isArray(report.checks)?report.checks:[];
  $('#diagnosticResults').innerHTML=checks.map(check=>`<div class="diagnostic-check ${escapeAttr(check.status)}"><span class="check-dot"></span><div><b>${escapeHTML(check.name)}</b><p>${escapeHTML(check.detail)}</p></div><span class="check-status">${escapeHTML(check.status)}${check.durationMs?' · '+check.durationMs+'ms':''}</span></div>`).join('')||'<p class="hint">No diagnostic results.</p>';
}
async function runDiagnostics(profileId,automatic=false) {
  if(diagnosticRunning||!profileId)return;
  diagnosticRunning=true;const button=$('#runDiagnostics');button.disabled=true;button.textContent='Checking…';
  try { const report=await req('/api/v1/diagnostics/run?profileId='+encodeURIComponent(profileId),{method:'POST'});renderDiagnostics(report);if(automatic)lastDiagnosticSession=currentStatus.startedAt||'' }
  catch(error) { $('#diagnosticResults').innerHTML=`<p class="form-error">${escapeHTML(error.message)}</p>` }
  finally { diagnosticRunning=false;button.disabled=false;button.textContent='Run now' }
}
function renderLogs() { const search=$('#logSearch').value.toLowerCase();$('#logs').textContent=logEntries.filter(e=>!search||`${e.level} ${e.component} ${e.message}`.toLowerCase().includes(search)).slice().reverse().map(e=>`${e.time} [${e.level}] ${e.component}: ${e.message}`).join('\n');$('#logs').scrollTop=0 }
function log(entry) { logEntries.push(entry);if(logEntries.length>2000)logEntries=logEntries.slice(-2000);renderLogs() }
async function openAuth(id) { selected = id; await loadRoutes(); const p = await req('/api/v1/profiles/' + id); $('#openServerScripts').hidden = !p.saveServerScripts; $('#auth form').reset(); $('#authError').textContent = ''; $('#authUsername').value = p.username || ''; $('#password').placeholder = p.passwordSet ? 'Saved in Keychain — leave blank to use' : 'Password'; $('#authHint').textContent = p.passwordSet ? 'The saved password will be used when this field is blank. OTP is never saved.' : 'Enter the password to connect. OTP is never saved.'; $('#auth').showModal(); $('#otp').focus() }
async function openEditor(id) { const p = await req('/api/v1/profiles/' + id); for (const key of ['id','name','username','protocol']) $('#' + key).value = p[key] || ''; $('#url').value = [p.server,...(p.fallbackServers||[])].join('\n'); $('#macAddress').value = p.macAddress || ''; $('#verbose').checked = !!p.verbose; $('#saveServerScripts').checked = !!p.saveServerScripts; $('#routeAdditions').value=(p.routeAdditions||[]).join('\n');$('#routeDeletions').value=(p.routeDeletions||[]).join('\n'); $('#profilePassword').value = ''; $('#profilePassword').placeholder = p.passwordSet ? 'Saved in Keychain — leave blank to keep' : 'Enter password to save'; $('#editor').showModal() }
document.addEventListener('click', async event => { try { const d = event.target.dataset; const row=event.target.closest('[data-select]');if(row&&!event.target.closest('button')&&!['connecting','connected','disconnecting'].includes(currentStatus.state)){await openAuth(row.dataset.select)} if (d.connect) await openAuth(d.connect); if (d.disconnect!==undefined) { await req('/api/v1/disconnect',{method:'POST'}); await refresh() } if (d.edit) await openEditor(d.edit); if (d.profileDelete && confirm(`Delete profile “${d.profileName}”? The saved Keychain password will also be removed.`)) { await req('/api/v1/profiles/'+d.profileDelete,{method:'DELETE'});if(selected===d.profileDelete)selected='';await refresh() } if (d.routeSave) { const input = document.querySelector(`[data-route-value="${d.routeSave}"]`); await req('/api/v1/routes/' + d.routeSave, {method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({cidr:input.value})}); await loadRoutes() } if (d.routeDelete) { await req('/api/v1/routes/' + d.routeDelete, {method:'DELETE'}); await loadRoutes() } } catch (error) { alert(error.message) } });
document.addEventListener('keydown', event => { const row=event.target.closest('[data-select]');if(row&&event.target===row&&!['connecting','connected','disconnecting'].includes(currentStatus.state)&&(event.key==='Enter'||event.key===' ')){event.preventDefault();openAuth(row.dataset.select).catch(error=>alert(error.message))} });
$('#new').onclick = () => { $('#editor form').reset(); $('#id').value = ''; $('#profilePassword').placeholder = 'Enter password to save'; $('#editor').showModal() };
$('#editorCancel').onclick = () => $('#editor').close();
$('#authCancel').onclick = () => { $('#password').value=''; $('#otp').value=''; $('#authError').textContent=''; $('#auth').close() };
$('#otp').oninput = event => { event.target.value = event.target.value.replace(/\s+/g, '') };
$('#clear').onclick = () => { logEntries=[]; renderLogs() };
$('#logout').onclick = async () => { await req('/api/v1/logout',{method:'POST'}); document.body.innerHTML='<main><section><h2>Portal session closed</h2><p>Reopen OpenConnect from the menu bar to continue.</p></section></main>' };
$('#openServerScripts').onclick = async () => { try { await req('/api/v1/server-scripts/open',{method:'POST'}) } catch(error) { alert(error.message) } };
$('#openPostureFiles').onclick = async () => { try { await req('/api/v1/server-scripts/open',{method:'POST'}) } catch(error) { alert(error.message) } };
$('#runDiagnostics').onclick = () => runDiagnostics(currentStatus.profileId||selected);
$('#logSearch').oninput = renderLogs;
$('#auth form').onsubmit = async event => { event.preventDefault(); const button=$('#authenticate'),username=$('#authUsername').value,password=$('#password').value,otp=$('#otp').value.replace(/\s+/g,''); if(button.disabled)return; $('#authError').textContent='';button.disabled=true;button.textContent='Connecting…'; try { await req('/api/v1/connect',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({profileId:selected,username,password,otp})}); $('#password').value='';$('#otp').value='';$('#auth').close();await refresh() } catch(error) { $('#password').value='';$('#otp').value='';$('#authError').textContent=error.message.trim();$('#otp').focus() } finally { button.disabled=false;button.textContent='Connect' } };
$('#addRoute').onclick = async () => { try { await req('/api/v1/routes',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({cidr:$('#newRoute').value})});$('#newRoute').value='';loadRoutes() } catch(error) { alert(error.message) } };
$('#exportRoutes').onclick = () => { if(!selected)return alert('Select a profile first');const link=document.createElement('a');link.href='/api/v1/routes-export?profileId='+encodeURIComponent(selected);link.download='openconnect-custom-routes.json';link.click() };
$('#diagnostics').onclick = async () => { try { const response=await fetch('/api/v1/diagnostics',{method:'POST',headers:{'X-CSRF-Token':csrf}});if(!response.ok)throw new Error(await response.text());const link=document.createElement('a');link.href=URL.createObjectURL(await response.blob());link.download='openconnect-diagnostics.zip';link.click();setTimeout(()=>URL.revokeObjectURL(link.href),1000) } catch(error) { alert(error.message) } };
$('#save').onclick = async event => { event.preventDefault(); const servers=parseRoutes($('#url').value),p={id:$('#id').value,name:$('#name').value,server:servers[0]||'',fallbackServers:servers.slice(1),username:$('#username').value,protocol:$('#protocol').value,password:$('#profilePassword').value,macAddress:$('#macAddress').value,verbose:$('#verbose').checked,saveServerScripts:$('#saveServerScripts').checked,routeAdditions:parseRoutes($('#routeAdditions').value),routeDeletions:parseRoutes($('#routeDeletions').value)}; try { await req('/api/v1/profiles'+(p.id?'/'+p.id:''),{method:p.id?'PUT':'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(p)});$('#profilePassword').value='';$('#editor').close();await refresh();await loadRoutes() } catch(error) { $('#profilePassword').value='';alert(error.message) } };
async function poll() { try{await refresh()}catch(error){showStatusError('Status refresh failed: '+error.message)}finally{setTimeout(poll,2000)} }
async function init() { const session=await req('/api/v1/session');csrf=session.csrfToken;$('#buildCommit').textContent=session.buildCommit||'unknown';await refresh();const response=await req('/api/v1/logs');logEntries=Array.isArray(response)?response:[];renderLogs();await loadRoutes();const events=new EventSource('/api/v1/events');events.onmessage=event=>{const entry=JSON.parse(event.data);log(entry);if(entry.component==='Routing')loadRoutes()};setTimeout(poll,2000);setInterval(()=>document.querySelectorAll('[data-uptime]').forEach(node=>node.textContent=duration(currentStatus.startedAt)),1000) }
init().catch(error => alert(error.message));
