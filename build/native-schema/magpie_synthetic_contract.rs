// Original validation harness. Official API contract: openai/codex
// 970111f6cb62cc02fe36a82ea7d71b37a2889c97 (Apache-2.0; retain LICENSE/NOTICE).
// No official helper implementation is copied. No recorder, API or real home.
use codex_protocol::models::ResponseItem;
use codex_protocol::protocol::{HistoryPosition, SessionMeta, SessionMetaLine, SessionSource, ThreadHistoryMode};
use codex_protocol::ThreadId;
use codex_rollout::{parse_rollout_line, RolloutItem, RolloutLine};
use serde_json::{json, Value};
use std::{env, fs, path::Path};

const THREAD: &str = "11111111-1111-4111-8111-111111111111";
const A: &str = "22222222-2222-4222-8222-222222222222";
const B: &str = "33333333-3333-4333-8333-333333333333";
const CHILD: &str = "44444444-4444-4444-8444-444444444444";
const GRAND: &str = "55555555-5555-4555-8555-555555555555";
const STAMP: &str = "2026-10-31T00:00:00Z";
const OPAQUE: &str = "OPAQUE_SYNTHETIC_NOT_REAL";
fn id(s: &str) -> ThreadId { ThreadId::from_string(s).unwrap() }
fn record(ordinal: u64, item: RolloutItem) -> String {
    let line = RolloutLine { timestamp: STAMP.into(), ordinal: Some(ordinal), item };
    let bytes = serde_json::to_string(&line).unwrap();
    // Call the REAL persistence-boundary decoder, not serde on a local mirror.
    let parsed = parse_rollout_line(&bytes).unwrap();
    assert_eq!(serde_json::to_value(&parsed).unwrap(), serde_json::to_value(&line).unwrap());
    bytes + "\n"
}
fn meta(thread: &str, parent: Option<(&str, u64, usize)>, logical_parent: Option<&str>) -> String {
    let mut m = SessionMeta {
        id: id(thread), session_id: id(THREAD).into(), timestamp: STAMP.into(),
        cwd: "/synthetic/workspace".into(), originator: "magpie-synthetic-contract".into(),
        cli_version: "synthetic-not-a-client-version".into(), source: SessionSource::Exec,
        history_mode: ThreadHistoryMode::Paginated, ..SessionMeta::default()
    };
    if let Some((revision, ordinal, offset)) = parent {
        m.history_base = Some(HistoryPosition { thread_id: id(revision), end_ordinal_exclusive: ordinal, end_byte_offset: offset as u64 });
        m.forked_from_id = logical_parent.map(id);
        m.forked_from_ordinal_exclusive = Some(ordinal);
    }
    record(parent.map_or(0, |p| p.1), RolloutItem::SessionMeta(Box::new(SessionMetaLine { meta: m, git: None })))
}
fn response(ordinal: u64, value: Value) -> String {
    // Typed admission before serialization; the canonical decoder validates again.
    let item: ResponseItem = serde_json::from_value(value).unwrap();
    record(ordinal, RolloutItem::ResponseItem(item.into()))
}
fn validate(bytes: &str) {
    for line in bytes.lines() { parse_rollout_line(line).unwrap(); }
}
fn filename(thread: &str, revision: &str) -> String {
    let suffix = if thread == revision { thread.to_string() } else { format!("{thread}_{revision}") };
    format!("rollout-2026-10-31T00-00-00-{suffix}.jsonl")
}
#[test]
fn typed_fixture_and_real_parser_contract() {
    let output = env::var("MAGPIE_SYNTHETIC_OUTPUT").unwrap();
    let output = Path::new(&output);
    assert!(!output.exists(), "exclusive synthetic output only");
    fs::create_dir(output).unwrap();
    let specs: Vec<Value> = serde_json::from_str(include_str!("magpie_capsules.json")).unwrap();
    assert_eq!(specs.len(), 2);
    for spec in specs {
        let variant = spec["variant"].as_str().unwrap();
        assert!(matches!(variant, "shrink" | "grow"));
        let summary = spec["summary"].as_str().unwrap();
        let prefix = meta(THREAD, None, None);
        // These records are conserved verbatim: opaque is a sentinel, never native ciphertext proof.
        let conserved = response(2, json!({"type":"reasoning","id":"rs_synthetic","summary":[{"type":"summary_text","text":"保留"}],"encrypted_content":OPAQUE}))
            + &response(3, json!({"type":"function_call","name":"synthetic","arguments":"{\"text\":\"工具\"}","call_id":"call_synthetic"}))
            + &response(4, json!({"type":"function_call_output","call_id":"call_synthetic","output":"synthetic result"}));
        let old_item = json!({"type":"compaction","id":"cmp_synthetic","encrypted_content":spec["capsule"]});
        let old_record = response(1, old_item);
        // Exact independent replacement encoding matches the documented Go JSON encoder.
        // Build the payload explicitly, not from preview output, and retain its outer envelope.
        let escaped = serde_json::to_string(&format!("Context summary from previous turns:\n{summary}")).unwrap()
            .replace('<', "\\u003c").replace('>', "\\u003e").replace('&', "\\u0026");
        let payload = format!("{{\"content\":[{{\"text\":{escaped},\"type\":\"output_text\"}}],\"role\":\"assistant\",\"type\":\"message\"}}");
        let old_value: Value = serde_json::from_str(old_record.trim()).unwrap();
        // Find the serialized typed payload without depending on object key order.
        let typed_old: ResponseItem = serde_json::from_value(old_value["payload"].clone()).unwrap();
        let old_payload = serde_json::to_string(&typed_old).unwrap();
        assert_eq!(old_record.matches(&old_payload).count(), 1);
        let new_record = old_record.replacen(&old_payload, &payload, 1);
        let old_root = prefix.clone() + &old_record + &conserved;
        let new_root = prefix + &new_record + &conserved;
        assert_eq!(new_root.len() > old_root.len(), variant == "grow");
        let tail = response(6, json!({"type":"message","role":"user","content":[{"type":"input_text","text":"synthetic child"}]}));
        let child_old = meta(CHILD, Some((B, 5, old_root.len())), Some(THREAD)) + &tail;
        let child_new = meta(CHILD, Some((B, 5, new_root.len())), Some(THREAD)) + &tail;
        let grand_old = meta(GRAND, Some((CHILD, 7, child_old.len())), Some(CHILD));
        let grand_new = meta(GRAND, Some((CHILD, 7, child_new.len())), Some(CHILD));
        let cases = [(THREAD,A,old_root.clone(),old_root.clone()),(THREAD,B,old_root,new_root),
                     (CHILD,CHILD,child_old,child_new),(GRAND,GRAND,grand_old,grand_new)];
        for (thread, revision, before, after) in cases {
            for (phase, bytes) in [("before", before), ("after", after)] {
                validate(&bytes);
                let root = if revision == CHILD || revision == GRAND { "archived_sessions" } else { "sessions" };
                let dir = output.join(variant).join(phase).join(root);
                fs::create_dir_all(&dir).unwrap();
                fs::write(dir.join(filename(thread,revision)), bytes).unwrap();
            }
        }
        // A real negative schema control plus legacy fallback positive control.
        let line = meta(THREAD,None,None);
        let value: Value = serde_json::from_str(line.trim()).unwrap();
        for field in ["timestamp", "cwd", "originator", "cli_version"] {
            let mut invalid = value.clone();
            invalid["payload"].as_object_mut().unwrap().remove(field);
            assert!(parse_rollout_line(&invalid.to_string()).is_err(), "missing metadata {field}");
        }
        let mut invalid = value.clone(); invalid.as_object_mut().unwrap().remove("timestamp");
        assert!(parse_rollout_line(&invalid.to_string()).is_err());
        let mut legacy = value; legacy["payload"].as_object_mut().unwrap().remove("session_id");
        match parse_rollout_line(&legacy.to_string()).unwrap().item {
            RolloutItem::SessionMeta(m) => assert_eq!(m.meta.session_id.to_string(), THREAD),
            _ => panic!("not metadata"),
        }
    }
    tokio::runtime::Builder::new_current_thread().enable_all().build().unwrap().block_on(official_index_contract(output));
    println!("REAL_PARSER_PASS variants=2 revisions=4 phases=2 schema_negative=5 legacy_fallback=1");
}

// The official index OBSERVES references; it does not enforce Magpie's stricter
// migration admission. Every scan below is confined to a new synthetic TempDir.
fn copy_phase(source: &Path, destination: &Path) {
    for directory in ["sessions", "archived_sessions"] {
        let target = destination.join(directory);
        fs::create_dir(&target).unwrap();
        for entry in fs::read_dir(source.join(directory)).unwrap() {
            let entry = entry.unwrap();
            assert!(entry.file_type().unwrap().is_file());
            fs::copy(entry.path(), target.join(entry.file_name())).unwrap();
        }
    }
}
fn observed_ids(index: &codex_rollout::RolloutReferenceIndex) -> Vec<String> {
    let mut ids: Vec<String> = [THREAD, CHILD, GRAND].into_iter()
        .flat_map(|thread| index.rollouts_for_thread(id(thread)).map(|(revision, _)| revision.to_string()))
        .collect();
    ids.sort();
    ids
}
fn require_four(index: &codex_rollout::RolloutReferenceIndex) -> bool {
    // Independent fixed-fixture completeness oracle, NOT official rejection.
    observed_ids(index) == [A, B, CHILD, GRAND]
}
async fn official_index_contract(output: &Path) {
    use codex_rollout::RolloutReferenceIndex;
    for variant in ["shrink", "grow"] {
        for phase in ["before", "after"] {
            let source = output.join(variant).join(phase);
            let home = tempfile::TempDir::new().unwrap();
            copy_phase(&source, home.path());
            let index = RolloutReferenceIndex::scan(home.path()).await.unwrap();
            assert!(require_four(&index));
            let mut roots: Vec<String> = index.rollouts_for_thread(id(THREAD)).map(|(r,_)| r.to_string()).collect();
            roots.sort(); assert_eq!(roots, [A, B]);
            assert_eq!(index.reference_count(id(A)), 0);
            assert_eq!(index.reference_count(id(B)), 1);
            assert_eq!(index.reference_count(id(CHILD)), 1);
            assert_eq!(index.reference_count(id(GRAND)), 0);
            assert!(index.attached_child(&[id(THREAD),id(CHILD)]).is_none());
            for (child,parent,ordinal) in [(CHILD,B,5),(GRAND,CHILD,7)] {
                let base = index.history_base(id(child)).unwrap();
                assert_eq!(base.thread_id, id(parent));
                assert_eq!(base.end_ordinal_exclusive, ordinal);
                let parent_thread = if parent==B {THREAD} else {CHILD};
                let directory = if parent==B {"sessions"} else {"archived_sessions"};
                let bytes = fs::read(source.join(directory).join(filename(parent_thread,parent))).unwrap();
                assert_eq!(base.end_byte_offset, bytes.len() as u64);
                let child_bytes = fs::read_to_string(source.join("archived_sessions").join(filename(child,child))).unwrap();
                match parse_rollout_line(child_bytes.lines().next().unwrap()).unwrap().item {
                    RolloutItem::SessionMeta(m) => {
                        assert_eq!(m.meta.forked_from_ordinal_exclusive, Some(ordinal));
                        assert_eq!(m.meta.forked_from_id, Some(id(parent_thread)));
                    }, _ => panic!("missing metadata"),
                }
            }
            // Real scanner must not modify even the disposable copies.
            for directory in ["sessions","archived_sessions"] {
                for entry in fs::read_dir(source.join(directory)).unwrap() {
                    let entry=entry.unwrap();
                    assert_eq!(fs::read(entry.path()).unwrap(),fs::read(home.path().join(directory).join(entry.file_name())).unwrap());
                }
            }
        }
        let source=output.join(variant).join("after");
        let home=tempfile::TempDir::new().unwrap(); copy_phase(&source,home.path());
        // Duplicate immutable revision is retained once, not rejected by Codex.
        let root_b=home.path().join("sessions").join(filename(THREAD,B));
        fs::copy(&root_b,home.path().join("archived_sessions").join(filename(THREAD,B))).unwrap();
        let index=RolloutReferenceIndex::scan(home.path()).await.unwrap();
        assert!(require_four(&index)); assert_eq!(index.reference_count(id(B)),1);
        // Attached metadata is observed by stable thread ID, not rejected.
        let child_path=home.path().join("archived_sessions").join(filename(CHILD,CHILD));
        let bytes=fs::read_to_string(&child_path).unwrap();
        let (head,tail)=bytes.split_once('\n').unwrap();
        let mut value:Value=serde_json::from_str(head).unwrap();
        value["payload"]["attached_thread"]=json!(true);
        fs::write(&child_path,value.to_string()+"\n"+tail).unwrap();
        let index=RolloutReferenceIndex::scan(home.path()).await.unwrap();
        assert_eq!(index.attached_child(&[id(THREAD)]),Some(id(CHILD)));
        assert!(index.attached_child(&[id(B)]).is_none());
        // A stale cutoff is indexed unchanged. Magpie admission must reject it.
        let old_len=fs::metadata(output.join(variant).join("before/sessions").join(filename(THREAD,B))).unwrap().len();
        value["payload"]["history_base"]["end_byte_offset"]=json!(old_len);
        fs::write(&child_path,value.to_string()+"\n"+tail).unwrap();
        let index=RolloutReferenceIndex::scan(home.path()).await.unwrap();
        assert_eq!(index.history_base(id(CHILD)).unwrap().end_byte_offset,old_len);
        // An omitted terminal descendant cannot be inferred by a scanner.
        fs::remove_file(home.path().join("archived_sessions").join(filename(GRAND,GRAND))).unwrap();
        let index=RolloutReferenceIndex::scan(home.path()).await.unwrap();
        assert_eq!(observed_ids(&index).len(),3);
        assert!(!require_four(&index)); // independent manifest expectation catches it
    }
    println!("REAL_INDEX_PASS phases=4 revisions=4 observation_only=duplicate,attached,stale,missing_leaf");
}
