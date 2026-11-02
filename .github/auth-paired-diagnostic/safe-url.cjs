// Synthetic fixture diagnostics only. Never retain query, fragment, userinfo,
// headers, request bodies, opaque URL payloads or credential-shaped path parts.
function safeURL(value) {
  try {
    const u = new URL(value);
    if (!['http:', 'https:'].includes(u.protocol)) return {scheme:u.protocol,host:'[hidden]',path:'[hidden]'};
    const parts = u.pathname.split('/');
    let redactNext = false;
    const pathname = parts.map(part => {
      let decoded;
      try { decoded=decodeURIComponent(part); } catch { return '[hidden]'; }
      const hidden = redactNext || decoded.length>64 || /[^a-zA-Z0-9_.-]/.test(decoded);
      redactNext = /^(auth|token|secret|session|key|password)$/i.test(decoded);
      return hidden ? '[hidden]' : decoded;
    }).join('/');
    return {scheme:u.protocol,host:u.host,path:pathname.slice(0,240)};
  } catch { return {scheme:'[invalid]',host:'[hidden]',path:'[hidden]'}; }
}
module.exports={safeURL};
