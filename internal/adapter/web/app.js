"use strict";
const $ = id => document.getElementById(id);
const labels = {new:"等待首次保存",bound:"已绑定",paused:"同步暂停",pending:"等待写回确认"};
const reasons = {HISTORICAL_PROGRESS_UNBOUND:"已有历史进度，来源尚未绑定",EPUB_VERSION_CHANGED:"EPUB 版本变化，请重新获取；旧进度不会自动迁移",PROGRESS_SOURCE_UNKNOWN:"无法确认进度来源",UPSTREAM_PROGRESS_RESET:"检测到上游重置",PROGRESS_TIME_UNKNOWN:"没有可归属的位置时间",PROGRESS_TIME_AMBIGUOUS:"位置时间不明确",UPSTREAM_WRITE_UNCONFIRMED:"写入结果未确认，已暂停",WRITE_READBACK_MISMATCH:"写入读回不一致",INCOMPLETE_BINDING_WRITE:"上次写入未完成确认",BOOK_FILES_CHANGED:"书籍文件结构已变化"};
async function status() {
  const r=await fetch("/api/status",{cache:"no-store"});
  if(!r.ok){$("account").hidden=true;$("login").hidden=false;return;}
  const d=await r.json();$("account").hidden=false;$("login").hidden=true;
  $("username").textContent=d.username;$("contract").textContent=d.contract;
  $("reader-url").textContent=d.readerURL;$("opds-url").textContent=d.opdsURL;
  $("download-permission").textContent=d.canDownload?"当前账户可获取 EPUB。":"当前账户没有下载权限；已有授权书籍仍可同步。";
  $("books").replaceChildren();
  for(const b of d.books){const p=document.createElement("p");p.textContent=`Book ${b.bookId} · ${labels[b.state]||b.state} · ${reasons[b.code]||b.code||"无错误"} · 最近成功：${b.lastSuccess?new Date(b.lastSuccess).toLocaleString():"尚无"}`;$("books").append(p);}
}
$("login").addEventListener("submit",async e=>{e.preventDefault();$("message").textContent="正在连接…";const form=new FormData(e.target);try{const r=await fetch("/reader3/login",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({username:form.get("username"),password:form.get("password"),isLogin:true})});const d=await r.json();e.target.elements.password.value="";if(!r.ok||!d.isSuccess)throw new Error(d.errorMsg||"登录失败");$("token").textContent=d.data.accessToken;$("token-panel").hidden=false;$("message").textContent="已生成此设备令牌。";await status();}catch(e){$("message").textContent=e.message;}});
$("refresh").onclick=()=>status().catch(()=>{$("message").textContent="状态获取失败，请稍后重试。";});
$("logout").onclick=async()=>{const r=await fetch("/reader3/logout",{method:"POST"});if(r.ok){$("token").textContent="";$("token-panel").hidden=true;$("message").textContent="此设备令牌已撤销。";await status();}else{$("message").textContent="注销失败，请重试。";}};
status().catch(()=>{$("message").textContent="暂时无法连接服务。";});
