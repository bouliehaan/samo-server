(function(){const E="samo-token";let v=localStorage.getItem(E)||"",m=null;const l=document.getElementById("step-card"),T=document.getElementById("progress");function S(e){const t=["admin","libraries","scan","done"];Array.from(T.children).forEach(a=>{a.classList.remove("active","done");const s=t.indexOf(a.dataset.step),i=t.indexOf(e);s<i?a.classList.add("done"):s===i&&a.classList.add("active")})}async function b(){const e=await fetch("/api/v1/setup/status");if(!e.ok)throw new Error("status "+e.status);m=await e.json(),S(m.currentStep);const t={admin:"1 OF 3",libraries:"2 OF 3",scan:"3 OF 3",done:"COMPLETE"}[m.currentStep]||"—";document.getElementById("hostStatus").querySelector(".status-text").textContent="SETUP · STEP "+t,N()}async function o(e,t){t=t||{},t.headers=t.headers||{},t.body&&(t.headers["Content-Type"]="application/json"),v&&(t.headers.Authorization="Bearer "+v);const a=await fetch(e,t);if(a.status===204)return null;const s=await a.json().catch(()=>({}));if(!a.ok)throw new Error(s.error||"request failed: "+a.status);return s}function d(e){const t=l.querySelector(".error-line");if(t&&t.remove(),!e)return;const a=document.createElement("div");a.className="error-line",a.textContent="× "+e,l.appendChild(a)}function r(e){return String(e||"").replace(/[&<>"]/g,t=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"})[t])}function A(){l.innerHTML=`
        <div class="card-head"><span class="caret">&gt;</span> CREATE YOUR ACCOUNT</div>
        <h2>Pick a username and password.</h2>
        <p class="lede">This is how you'll sign in. Nothing leaves your machine — Samo stores credentials locally.</p>
        <div class="form-row-split">
          <label class="field">
            <span class="field-label">USERNAME</span>
            <input type="text" id="adminUsername" autocomplete="username" placeholder="jake">
          </label>
          <label class="field">
            <span class="field-label">PASSWORD</span>
            <input type="password" id="adminPassword" autocomplete="new-password" placeholder="8+ characters">
          </label>
        </div>
        <div class="actions">
          <button class="btn primary" id="adminSubmit">CREATE ACCOUNT &rarr;</button>
        </div>
      `;const e=()=>{const t=document.getElementById("adminUsername").value.trim(),a=document.getElementById("adminPassword").value;if(d(""),!t)return d("username is required");if(a.length<8)return d("password must be at least 8 characters");const s=document.getElementById("adminSubmit");s.disabled=!0,s.textContent="CREATING…",o("/api/v1/setup/admin",{method:"POST",body:JSON.stringify({username:t,password:a})}).then(i=>(v=i.token,localStorage.setItem(E,v),document.getElementById("hostSession").textContent="SIGNED IN · "+i.user.username.toUpperCase(),b())).catch(i=>{s.disabled=!1,s.textContent="CREATE ACCOUNT →",d(i.message)})};document.getElementById("adminSubmit").addEventListener("click",e),document.getElementById("adminPassword").addEventListener("keydown",t=>{t.key==="Enter"&&e()})}let c=[],h=[];async function y(){try{const e=await o("/api/v1/podcasts/feeds?limit=50",{method:"GET"});h=e&&e.items||[]}catch{h=[]}I()}function I(){const e=document.querySelector(".feeds-attached-body"),t=document.querySelector(".feeds-attached-head .count");if(e){if(t&&(t.textContent=h.length+" ADDED"),h.length===0){e.innerHTML='<div class="libs-empty">// no rss feeds yet · optional — add subscriptions for remote podcasts</div>';return}e.innerHTML="",h.forEach(a=>{const s=document.createElement("div");s.className="lib-row";const i=a.autoDownloadEnabled?" · AUTO-DOWNLOAD ON":"";s.innerHTML='<div class="lib-main"><div class="lib-name">'+r(a.title||a.feedUrl)+'</div><div class="lib-path">'+r(a.feedUrl)+i+"</div></div>",e.appendChild(s)})}}async function g(e){const t="/api/v1/setup/directories"+(e?"?path="+encodeURIComponent(e):""),a=await o(t,{method:"GET"});C(a)}function C(e){const t=document.querySelector(".browser-list"),a=document.querySelector(".browser-head .value");if(!t||!a)return;a.textContent=e.path||"SUGGESTED LOCATIONS",t.innerHTML="",(e.entries||[]).forEach(i=>{const n=document.createElement("div");n.className="browser-row"+(i.isParent?" is-parent":"");const u=document.createElement("div");u.textContent=i.isParent?".. /":i.name;const p=document.createElement("div");p.className="meta",i.isParent?p.textContent="PARENT":i.isRoot?p.textContent="SHORTCUT":i.itemCount?p.textContent=i.itemCount+" ITEMS":p.textContent="EMPTY",n.appendChild(u),n.appendChild(p),n.addEventListener("click",()=>g(i.path)),t.appendChild(n)});const s=document.getElementById("libraryPath");if(s&&e.path){s.value=e.path;const i=document.getElementById("libraryName");if(i&&!i.dataset.touched){const n=e.path.split("/").filter(Boolean);i.placeholder=n.length?n[n.length-1]:"autodetect"}}}async function f(){try{const a=await o("/api/v1/libraries",{method:"GET"});c=a&&a.items||[]}catch(a){c=[],d(a.message)}w();const e=document.getElementById("librariesContinue");e&&(e.disabled=c.length===0);const t=document.querySelector(".libs-attached-head .count");t&&(t.textContent=c.length+" ATTACHED")}function w(){const e=document.querySelector(".libs-attached-body");if(e){if(c.length===0){e.innerHTML='<div class="libs-empty">// no folders attached yet · pick one below and Samo will index it on the next step</div>';return}e.innerHTML="",c.forEach(t=>{const a=document.createElement("div");a.className="lib-row";const s=t.kind==="mixed"?"MIXED":t.kind==="music"?"MUSIC":t.kind==="audiobook"?"AUDIOBOOKS":t.kind==="podcast"?"PODCASTS":t.kind.toUpperCase();a.innerHTML='<div class="lib-main"><div class="lib-name">'+r(t.name)+'<span class="kind-chip">'+s+'</span></div><div class="lib-path">'+r(t.path)+'</div></div><button class="btn-remove" title="Remove" data-id="'+r(t.id)+'">×</button>',e.appendChild(a)}),e.querySelectorAll(".btn-remove").forEach(t=>{t.addEventListener("click",async a=>{const s=a.currentTarget.getAttribute("data-id");try{await o("/api/v1/libraries/"+encodeURIComponent(s),{method:"DELETE"}),await f(),await b()}catch(i){d(i.message)}})})}}function O(){l.innerHTML=`
        <div class="card-head"><span class="caret">&gt;</span> ATTACH YOUR MEDIA</div>
        <h2>Where is your stuff?</h2>
        <p class="lede">Add one or more folders. <strong>Mixed</strong> auto-detects music vs. audiobooks per subfolder — pick that if you have a single folder with everything, or you're not sure.</p>

        <div class="libs-shell">
          <div class="libs-attached">
            <div class="libs-attached-head">
              <span class="label">ATTACHED LIBRARIES</span>
              <span class="count">0 ATTACHED</span>
            </div>
            <div class="libs-attached-body"></div>
          </div>

          <div class="libs-add">
            <div class="libs-add-head">+ ADD A FOLDER</div>
            <div class="libs-add-body">
              <div class="browser-shell">
                <div class="browser-head"><span class="label">PATH</span><span class="value">SUGGESTED LOCATIONS</span></div>
                <div class="browser-list"></div>
              </div>
              <label class="field">
                <span class="field-label">PATH</span>
                <input type="text" id="libraryPath" placeholder="/srv/media">
              </label>
              <div class="form-row-split">
                <label class="field">
                  <span class="field-label">KIND</span>
                  <select id="libraryKind">
                    <option value="mixed">MIXED (AUTO-DETECT)</option>
                    <option value="music">MUSIC ONLY</option>
                    <option value="audiobook">AUDIOBOOKS</option>
                    <option value="podcast">PODCASTS</option>
                  </select>
                </label>
                <label class="field">
                  <span class="field-label">NAME (OPTIONAL)</span>
                  <input type="text" id="libraryName" placeholder="autodetect">
                </label>
              </div>
              <div class="actions">
                <button class="btn primary" id="libraryAdd">+ ATTACH THIS FOLDER</button>
              </div>
            </div>
          </div>

          <div class="libs-add" style="margin-top: 18px;">
            <div class="libs-add-head">+ PODCAST RSS FEEDS (OPTIONAL)</div>
            <div class="libs-add-body">
              <div class="feeds-attached">
                <div class="feeds-attached-head">
                  <span class="label">ADDED FEEDS</span>
                  <span class="count">0 ADDED</span>
                </div>
                <div class="feeds-attached-body"></div>
              </div>
              <label class="field">
                <span class="field-label">FEED URL</span>
                <input type="url" id="podcastFeedURL" placeholder="https://example.com/feed.xml">
              </label>
              <label class="field">
                <span class="field-label">TITLE (OPTIONAL)</span>
                <input type="text" id="podcastFeedTitle" placeholder="show name">
              </label>
              <label class="field" style="display:flex; align-items:center; gap:10px; margin-top:8px;">
                <input type="checkbox" id="podcastFeedAutoDownload">
                <span>AUTO-DOWNLOAD NEW EPISODES AS THEY APPEAR</span>
              </label>
              <div class="actions">
                <button class="btn primary" id="podcastFeedAdd">+ ADD FEED</button>
              </div>
            </div>
          </div>

          <div class="continue-row">
            <button class="btn ghost" id="librariesContinue" disabled>CONTINUE TO SCAN &rarr;</button>
          </div>
        </div>
      `,g("").catch(e=>d(e.message)),f(),y(),document.getElementById("libraryName").addEventListener("input",e=>{e.target.dataset.touched="1"}),document.getElementById("podcastFeedAdd").addEventListener("click",async()=>{const e=document.getElementById("podcastFeedURL").value.trim(),t=document.getElementById("podcastFeedTitle").value.trim(),a=document.getElementById("podcastFeedAutoDownload").checked;if(d(""),!e)return d("paste a podcast feed url first");const s=document.getElementById("podcastFeedAdd");s.disabled=!0;const i=s.textContent;s.textContent="ADDING…";try{await o("/api/v1/podcasts/feeds",{method:"POST",body:JSON.stringify({url:e,title:t,autoDownloadEnabled:a})}),document.getElementById("podcastFeedURL").value="",document.getElementById("podcastFeedTitle").value="",await y()}catch(n){d(n.message)}finally{s.disabled=!1,s.textContent=i}}),document.getElementById("libraryAdd").addEventListener("click",async()=>{const e=document.getElementById("libraryPath").value.trim(),t=document.getElementById("libraryName").value.trim(),a=document.getElementById("libraryKind").value;if(d(""),!e)return d("pick a folder first — browse above or paste a path");const s=document.getElementById("libraryAdd");s.disabled=!0;const i=s.textContent;s.textContent="ATTACHING…";try{await o("/api/v1/setup/libraries",{method:"POST",body:JSON.stringify({path:e,name:t,kind:a})}),document.getElementById("libraryPath").value="";const n=document.getElementById("libraryName");n.value="",delete n.dataset.touched,await f()}catch(n){d(n.message)}finally{s.disabled=!1,s.textContent=i}}),document.getElementById("librariesContinue").addEventListener("click",async()=>{if(c.length===0){d("attach at least one folder first");return}await b()})}function L(){l.innerHTML=`
        <div class="card-head"><span class="caret">&gt;</span> INDEX YOUR MEDIA</div>
        <h2>One last thing — let's build the catalog.</h2>
        <p class="lede">Samo reads each file once to learn what it has. Large libraries take a few minutes. You can skip and come back to this later from settings.</p>
        <div class="actions">
          <button class="btn primary" id="scanRun">RUN INITIAL SCAN</button>
          <button class="btn ghost" id="finishLater">SKIP FOR NOW</button>
        </div>
        <div id="scanOutput" style="margin-top: 18px;"></div>
      `,document.getElementById("scanRun").addEventListener("click",async()=>{const e=document.getElementById("scanRun");e.disabled=!0;const t=e.textContent;e.textContent="SCANNING…";const a=document.getElementById("scanOutput");a.innerHTML='<div class="scan-output">// kicking off scan…</div>',d("");let s="";try{const n=await o("/api/v1/setup/scan",{method:"POST"});if(s=n&&n.job&&n.job.id,!s)throw new Error("scan job not returned")}catch(n){a.innerHTML="",d(n.message),e.disabled=!1,e.textContent=t;return}const i=async()=>{let n;try{n=await o("/api/v1/scan/jobs/"+encodeURIComponent(s),{method:"GET"})}catch(u){a.innerHTML="",d(u.message),e.disabled=!1,e.textContent=t;return}if(n.status==="running"||n.status==="pending"){a.innerHTML='<div class="scan-output">// indexing… '+(n.filesSeen||0)+" files seen</div>",setTimeout(i,1200);return}if(n.status==="completed"){const u=[(n.filesSeen||0)+" files"];n.itemsPruned&&u.push(n.itemsPruned+" items pruned"),a.innerHTML='<div class="scan-output success">// scan complete · '+u.join(" · ")+"</div>",await b()}else a.innerHTML='<div class="scan-output" style="color: var(--danger); border-color: var(--danger);">// scan failed: '+r(n.error||"unknown error")+"</div>";e.disabled=!1,e.textContent=t};setTimeout(i,600)}),document.getElementById("finishLater").addEventListener("click",async()=>{try{await o("/api/v1/setup/complete",{method:"POST"})}catch(e){d(e.message);return}window.location.href="/"})}function D(){l.innerHTML=`
        <div class="card-head"><span class="caret">&gt;</span> READY</div>
        <h2>Samo is live.</h2>
        <p class="lede">Catalog seeded. Your token is stored in this browser — clear site data to sign out. Open the dashboard to start listening.</p>
        <div class="actions">
          <a class="btn primary" href="/">OPEN DASHBOARD &rarr;</a>
        </div>
      `}function N(){if(m)switch(m.currentStep){case"admin":A();break;case"libraries":O();break;case"scan":L();break;default:D()}}b().catch(e=>{l.innerHTML='<div class="card-head"><span class="caret">&gt;</span> ERROR</div><h2>Setup unavailable</h2><p class="lede">'+r(e.message)+"</p>"})})();
//# sourceMappingURL=setup-CEjR3d98.js.map
