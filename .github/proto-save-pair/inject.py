"""Test-fixture-only deterministic POST gate; never changes application assets."""
def once(text, old, new):
 assert text.count(old)==1,repr(old)
 return text.replace(old,new)
def instrument(text, mode):
 assert mode in ('delayed','wrong-payload','http-error','aborted-post')
 text=once(text,'function server(lang, posts) {','function server(lang, posts, gate) {')
 text=once(text,'        const body = req.postDataJSON();', '''        await gate.ready;
        gate.seen++;
        if (gate.mode === "aborted-post") return route.abort();
        if (gate.mode === "http-error") return route.fulfill({status: 503, json: {error: "synthetic failure"}});
        const body = gate.mode === "wrong-payload" ? {trayUsages: ["incorrect"]} : req.postDataJSON();''')
 text=once(text,'      const errors = [], posts = [];', '''      const errors = [], posts = [];
      let release;
      const gate = {mode: MODE, seen: 0, ready: new Promise((r) => {release = r;})};'''.replace('MODE',repr(mode)))
 text=once(text,'server(lang, posts)','server(lang, posts, gate)')
 text=once(text,'      await page.keyboard.press("Escape");','''      try { await page.keyboard.press("Escape"); }
      finally { setImmediate(release); }''')
 # The old synchronous assertion executes before this event-loop release.
 # The new response waiter yields until the very same mocked route completes.
 return text
