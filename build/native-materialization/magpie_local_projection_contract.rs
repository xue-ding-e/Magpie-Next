// Original synthetic integration harness, not a copied official implementation.
// Public API pin: openai/codex 970111f6cb62cc02fe36a82ea7d71b37a2889c97.
// Retain upstream Apache-2.0 LICENSE/NOTICE when provisioned into its test target.
// No provider/session manager, real HOME, commands or private materializer are used.
use codex_app_server_protocol::ThreadItem;
use codex_protocol::items::{AgentMessageContent, AgentMessageItem, DynamicToolCallItem,
    DynamicToolCallStatus, FunctionCallOutputItem, ReasoningItem, TurnItem, UserMessageItem};
use codex_protocol::models::{BaseInstructions, ContentItem, FunctionCallOutputBody, ResponseItem};
use codex_protocol::protocol::{EventMsg, HistoryPosition, ItemCompletedEvent, SessionSource,
    ThreadHistoryMode, ThreadMemoryMode, TurnCompleteEvent, TurnStartedEvent};
use codex_protocol::user_input::UserInput;
use codex_protocol::ThreadId;
use codex_rollout::{parse_rollout_line, RolloutItem, RolloutLine, RolloutReferenceIndex};
use codex_state::{SqliteConfig, StateRuntime};
use codex_thread_store::{AppendThreadItemsParams, CreateThreadParams, ForkBoundary, ItemSortKey,
    ListItemsParams, ListTurnsParams, LoadThreadHistoryParams, LocalThreadStore,
    LocalThreadStoreConfig, PersistContext, PrepareForkParams, ResumeThreadParams,
    RevertThreadParams, SortDirection, StoredTurnItemsView, StoredTurnStatus,
    ThreadPersistenceMetadata, ThreadStore};
use codex_utils_absolute_path::AbsolutePathBuf;
use serde_json::{json, Value};
use sqlx::SqlitePool;
use std::{collections::BTreeMap, env, fs, path::{Path, PathBuf}, time::Duration};
use tempfile::TempDir;

const ROOT: &str = "11111111-1111-4111-8111-111111111111";
const CHILD: &str = "44444444-4444-4444-8444-444444444444";
const GRAND: &str = "55555555-5555-4555-8555-555555555555";
const PROVIDER: &str = "synthetic-no-provider";
fn id(s: &str) -> ThreadId { ThreadId::from_string(s).unwrap() }
fn metadata(cwd: &Path) -> ThreadPersistenceMetadata {
    ThreadPersistenceMetadata { cwd: Some(cwd.to_owned()), model_provider: PROVIDER.into(),
        memory_mode: ThreadMemoryMode::Disabled }
}
fn create(thread: ThreadId, cwd: &Path, parent: Option<ThreadId>, base: Option<HistoryPosition>) -> CreateThreadParams {
    CreateThreadParams { creator_user_id: None, creator_account_id: None, session_id: id(ROOT).into(),
        thread_id: thread, extra_config: None, forked_from_id: parent, attached_thread: false,
        parent_thread_id: None, source: SessionSource::Exec, thread_source: None,
        originator: "magpie-local-synthetic".into(), base_instructions: BaseInstructions::default(),
        dynamic_tools: vec![], selected_capability_roots: vec![], multi_agent_version: None,
        history_mode: ThreadHistoryMode::Paginated, history_base: base,
        subagent_history_start_ordinal: None, initial_window_id: "synthetic-window".into(),
        runtime_workspace_roots: None, metadata: metadata(cwd) }
}
fn raw_message(text: &str) -> RolloutItem {
    RolloutItem::ResponseItem(ResponseItem::Message { encrypted_content: None, status: None,
        id: None, role: "assistant".into(), content: vec![ContentItem::OutputText { text: text.into(), annotations: None, logprobs: None }],
        phase: None, internal_chat_message_metadata_passthrough: None }.into())
}
fn turn(thread: ThreadId, name: &str, raw: Vec<RolloutItem>, summary: Option<&str>) -> Vec<RolloutItem> {
    let rich=summary.is_some();
    let mut result = vec![RolloutItem::EventMsg(EventMsg::TurnStarted(TurnStartedEvent {
        turn_attribution: None, turn_id: name.into(), root_turn_id: Some("causal-root".into()),
        trace_id: None, started_at: Some(10), model_context_window: None,
        collaboration_mode_kind: Default::default() }))];
    let mut projected = vec![TurnItem::UserMessage(UserMessageItem { id: format!("{name}-user"),
        client_id: None, content: vec![UserInput::Text { text: format!("synthetic {name}"), text_elements: vec![] }] })];
    result.extend(raw);
    if rich {
        projected.extend([
            TurnItem::Reasoning(ReasoningItem { id: "rs_synthetic".into(), summary_text: vec!["保留".into()],
                raw_content: vec!["synthetic visible reasoning".into()] }),
            // Real dynamic handler uses call_id as this id. This is data, not a tool invocation.
            TurnItem::DynamicToolCall(DynamicToolCallItem { id: "call_synthetic".into(), namespace: None,
                tool: "synthetic".into(), arguments: json!({"text":"工具"}),
                status: DynamicToolCallStatus::Completed, content_items: None, success: Some(true),
                error: None, duration: Some(Duration::from_millis(7)) }),
            TurnItem::FunctionCallOutput(FunctionCallOutputItem { id: "output_synthetic".into(),
                name: "synthetic".into(), namespace: None,
                output: FunctionCallOutputBody::Text("synthetic result".into()) }),
        ]);
    }
    projected.push(TurnItem::AgentMessage(AgentMessageItem { id: format!("{name}-agent"),
        content: vec![AgentMessageContent::Text { text: summary.map_or_else(||format!("completed {name}"),String::from) }],
        phase: None, memory_citation: None, delivery: None, questions: None }));
    result.extend(projected.into_iter().map(|item| RolloutItem::EventMsg(EventMsg::ItemCompleted(
        ItemCompletedEvent { thread_id: thread, turn_id: name.into(), item, started_at_ms: Some(10_000),
            completed_at_ms: 20_000 }))));
    result.push(RolloutItem::EventMsg(EventMsg::TurnComplete(TurnCompleteEvent {
        root_turn_id: Some("causal-root".into()), turn_id: name.into(), last_agent_message: None,
        error: None, started_at: Some(10), completed_at: Some(20), duration_ms: Some(10_000),
        time_to_first_token_ms: None })));
    result
}
fn lines(path: &Path) -> Vec<(RolloutLine, u64, u64)> {
    let bytes = fs::read_to_string(path).unwrap();
    assert!(bytes.ends_with('\n'));
    let mut offset = 0;
    bytes.split_inclusive('\n').map(|text| {
        let start = offset; offset += text.len() as u64;
        (parse_rollout_line(text.trim_end_matches('\n')).unwrap(), start, offset)
    }).collect()
}
fn raw_values(items: &[RolloutItem]) -> Vec<Value> {
    items.iter().filter_map(|i| match i {
        RolloutItem::ResponseItem(r) => Some(serde_json::to_value(&r.item).unwrap()), _ => None,
    }).collect()
}
// One exact checker is shared by the positive case and independent assertion-oracle mutants.
fn retained_raw_matches(actual: &[Value], expected: &[Value]) -> bool {
    expected.iter().all(|e| actual.iter().filter(|a| *a == e).count() == 1)
}
async fn seed(state: &StateRuntime, thread: ThreadId, path: &Path) {
    // Public canonical-file repair, not a fabricated metadata/pointer upsert.
    codex_rollout::state_db::reconcile_rollout(Some(state), path, PROVIDER, None, &[], Some(false), None).await;
    let row = state.get_thread(thread).await.unwrap().expect("metadata seed actually succeeded");
    assert_eq!(row.rollout_path, path);
    assert_eq!(row.history_mode, ThreadHistoryMode::Paginated);
}
async fn append(store: &LocalThreadStore, thread: ThreadId, items: Vec<RolloutItem>) {
    store.append_items(AppendThreadItemsParams { thread_id: thread, items }).await.unwrap();
    store.flush_thread(thread).await.unwrap();
}
async fn check_projection(pool: &SqlitePool, path: &Path) -> Value {
    let revision = codex_rollout::rollout_id_from_path(path).unwrap().to_string();
    let parsed = lines(path);
    let mut expected_items = vec![];
    let mut normalized = vec![];
    let mut turn_count=0i64;
    for (line, start, end) in &parsed {
        let ordinal = line.ordinal.unwrap();
        if let RolloutItem::EventMsg(EventMsg::ItemCompleted(event)) = &line.item {
            let item = ThreadItem::from(event.item.clone());
            let value = serde_json::to_value(item).unwrap();
            let item_id = value["id"].as_str().unwrap().to_string();
            expected_items.push((event.turn_id.clone(), item_id.clone(), ordinal as i64, value));
            normalized.push(json!(["item", event.turn_id, item_id, ordinal]));
        }
        if let RolloutItem::EventMsg(EventMsg::TurnStarted(event)) = &line.item {
            turn_count+=1;
            let finish = parsed.iter().find(|(l,_,_)| matches!(&l.item,
                RolloutItem::EventMsg(EventMsg::TurnComplete(e)) if e.turn_id == event.turn_id)).unwrap();
            let row: (i64, i64, i64, i64, String, i64, i64, i64, String, String, String) =
                sqlx::query_as("SELECT rollout_ordinal,rollout_byte_offset,rollout_end_ordinal,rollout_end_byte_offset,status,started_at,completed_at,duration_ms,first_user_item_id,final_agent_item_id,root_turn_id FROM thread_turns WHERE thread_id=? AND turn_id=?")
                    .bind(&revision).bind(&event.turn_id).fetch_one(pool).await.unwrap();
            assert_eq!(row, (ordinal as i64, *start as i64, finish.0.ordinal.unwrap() as i64,
                finish.2 as i64, "completed".into(), 10, 20, 10_000, format!("{}-user",event.turn_id),
                format!("{}-agent",event.turn_id), "causal-root".into()));
            normalized.push(json!(["turn", event.turn_id, ordinal, finish.0.ordinal.unwrap()]));
        }
        assert!(*end > *start);
    }
    let rows: Vec<(String,String,i64,String)> = sqlx::query_as(
        "SELECT turn_id,item_id,rollout_ordinal,item_json FROM thread_items WHERE thread_id=? ORDER BY rollout_ordinal")
        .bind(&revision).fetch_all(pool).await.unwrap();
    let stored_turn_count:i64=sqlx::query_scalar("SELECT COUNT(*) FROM thread_turns WHERE thread_id=?").bind(&revision).fetch_one(pool).await.unwrap();
    assert_eq!(stored_turn_count,turn_count);
    assert!(!rows.is_empty(), "flush Ok is not projection proof");
    assert_eq!(rows.len(), expected_items.len());
    for (actual, expected) in rows.iter().zip(&expected_items) {
        assert_eq!((&actual.0,&actual.1,actual.2), (&expected.0,&expected.1,expected.2));
        assert_eq!(serde_json::from_str::<Value>(&actual.3).unwrap(), expected.3);
    }
    let checkpoint: (i64,i64) = sqlx::query_as(
        "SELECT next_rollout_byte_offset,next_rollout_ordinal FROM thread_history_projection_state WHERE thread_id=?")
        .bind(&revision).fetch_one(pool).await.unwrap();
    assert_eq!(checkpoint, (fs::metadata(path).unwrap().len() as i64, parsed.last().unwrap().0.ordinal.unwrap() as i64+1));
    json!({"ordinals":normalized,"next_ordinal":checkpoint.1,"next_byte_offset":checkpoint.0})
}
async fn visible(store: &LocalThreadStore, thread: ThreadId, expected_turns: &[&str]) {
    let turns = store.list_turns(ListTurnsParams { thread_id: thread, include_archived: false,
        cursor: None, page_size: 100, sort_direction: SortDirection::Asc,
        items_view: StoredTurnItemsView::NotLoaded }).await.unwrap();
    assert!(turns.next_cursor.is_none());
    assert_eq!(turns.turns.iter().map(|t| t.turn_id.as_str()).collect::<Vec<_>>(), expected_turns);
    assert!(turns.turns.iter().all(|t| matches!(t.status,StoredTurnStatus::Completed)));
    let page = store.list_items(ListItemsParams { item_ids: None, thread_id: thread, turn_id: None,
        include_archived: false, position: None, page_size: 100, sort_direction: SortDirection::Asc,
        sort_key: ItemSortKey::CreatedAtOrdinal, after_updated_at_ordinal: None }).await.unwrap();
    assert!(page.next_cursor.is_none());
    let mut expected = vec![];
    for turn in expected_turns {
        expected.push(format!("{turn}-user"));
        if *turn=="A-retained" { expected.extend(["rs_synthetic","call_synthetic","output_synthetic"].map(String::from)); }
        expected.push(format!("{turn}-agent"));
    }
    assert_eq!(page.items.iter().map(|i| i.item_id.clone()).collect::<Vec<_>>(), expected);
    let call = page.items.iter().find(|i| i.item_id=="call_synthetic").unwrap();
    let call: Value = serde_json::from_slice(&call.item_json).unwrap();
    assert_eq!(call["tool"],"synthetic"); assert_eq!(call["arguments"],json!({"text":"工具"}));
    assert_eq!(call["status"],"completed"); assert_eq!(call["success"],true); assert_eq!(call["durationMs"],7);
    let reasoning = page.items.iter().find(|i| i.item_id=="rs_synthetic").unwrap();
    let reasoning: Value = serde_json::from_slice(&reasoning.item_json).unwrap();
    assert_eq!(reasoning["summary"],json!(["保留"]));
    assert_eq!(reasoning["content"],json!(["synthetic visible reasoning"]));
    // No encrypted_content assertion on UI projection: it intentionally has no such field.
}
fn matches_boundary(base: HistoryPosition, revision: ThreadId, ordinal: u64, bytes: u64) -> bool {
    base.thread_id==revision && base.end_ordinal_exclusive==ordinal && base.end_byte_offset==bytes
}
async fn fork(store: &LocalThreadStore, state: &StateRuntime, cwd: &Path,
    source: ThreadId, child: ThreadId, expected_revision: ThreadId) -> PathBuf {
    let source_path=store.live_rollout_path(source).await.unwrap();
    let source_lines=lines(&source_path);
    let expected_ordinal=source_lines.last().unwrap().0.ordinal.unwrap()+1;
    let expected_bytes=fs::metadata(&source_path).unwrap().len();
    let prepared = store.prepare_fork(PrepareForkParams { thread_id: source, boundary: ForkBoundary::Latest }).await.unwrap();
    let base = prepared.history_base.expect("nonempty real prepared prefix");
    assert_eq!(prepared.source_thread_id,source);
    assert!(matches_boundary(base,expected_revision,expected_ordinal,expected_bytes));
    // Assertion-oracle negative only; never mutate official files/index/DB to fake a native result.
    let wrong_base=HistoryPosition { thread_id:id(ROOT), ..base };
    assert_ne!(expected_revision,id(ROOT));
    assert!(!matches_boundary(wrong_base,expected_revision,expected_ordinal,expected_bytes));
    store.create_thread(create(child,cwd,Some(source),Some(base))).await.unwrap();
    store.persist_thread(child,PersistContext::Standard).await.unwrap();
    let path = store.live_rollout_path(child).await.unwrap();
    let meta = codex_rollout::read_session_meta_line(&path).await.unwrap().meta;
    assert_eq!(meta.history_base,Some(base)); assert_eq!(meta.id,child);
    assert_eq!(meta.forked_from_id,Some(source)); assert_eq!(meta.session_id.to_string(),ROOT);
    // Retain the actual reservation through durable creation; never forge PreparedFork.
    drop(prepared);
    seed(state,child,&path).await;
    path
}
fn assert_owned_env(base: &Path) {
    // Controller must supply fresh synthetic roots. Refuse an ambient home/config/cache.
    for name in ["HOME","CODEX_HOME","XDG_CONFIG_HOME","XDG_CACHE_HOME","XDG_DATA_HOME","TMPDIR","CARGO_HOME"] {
        let path=PathBuf::from(env::var_os(name).unwrap_or_else(||panic!("missing {name}")));
        assert!(path.is_absolute() && path.starts_with(base) && path != base, "unowned {name}");
        assert!(!fs::symlink_metadata(&path).unwrap().file_type().is_symlink());
        assert_eq!(fs::canonicalize(&path).unwrap(),path,"noncanonical synthetic environment path");
    }
}
#[tokio::test(flavor="current_thread")]
async fn native_local_projection_four_revisions() {
    let base=PathBuf::from(env::var_os("MAGPIE_OWNED_ROOT").unwrap());
    assert!(base.is_absolute()); assert!(!fs::symlink_metadata(&base).unwrap().file_type().is_symlink());
    assert_eq!(fs::canonicalize(&base).unwrap(),base);
    assert_owned_env(&base);
    let input=PathBuf::from(env::var_os("MAGPIE_GOLDEN_INPUT").unwrap());
    let output=PathBuf::from(env::var_os("MAGPIE_MATERIALIZATION_OUTPUT").unwrap());
    assert!(input.is_absolute() && input.starts_with(&base) && input!=base);
    assert_eq!(fs::canonicalize(&input).unwrap(),input);
    let parent=output.parent().unwrap();
    assert!(output.is_absolute() && parent.starts_with(&base) && !output.exists());
    assert_eq!(fs::canonicalize(parent).unwrap(),parent);
    fs::create_dir(&output).unwrap(); assert_eq!(fs::canonicalize(&output).unwrap(),output);
    let references: BTreeMap<String,String>=serde_json::from_str(include_str!("magpie_reference_hashes.json")).unwrap();
    assert_eq!(references.len(),16);
    // External Python audit checks SHA256; here retain every original byte for post-test comparison.
    let originals: BTreeMap<_,_>=references.keys().map(|p| {
        let path=input.join(p); assert_eq!(fs::canonicalize(&path).unwrap(),path);
        assert!(path.starts_with(&input)); (p.clone(),fs::read(path).unwrap())
    }).collect();
    let mut report=BTreeMap::new();
    for variant in ["shrink","grow"] { for phase in ["before","after"] {
        let name=format!("{variant}/{phase}");
        let sandbox=TempDir::new_in(&base).unwrap();
        let home=sandbox.path().join("codex"); let cwd=sandbox.path().join("workspace");
        fs::create_dir(&home).unwrap(); fs::create_dir(&cwd).unwrap();
        for ancestor in cwd.ancestors() { assert!(!ancestor.join(".git").exists(),"Git metadata subprocess branch forbidden"); }
        let sqlite=SqliteConfig::new_for_testing(AbsolutePathBuf::try_from(sandbox.path().join("db")).unwrap());
        let state=StateRuntime::init(sqlite.clone(),PROVIDER.into()).await.unwrap();
        let store=LocalThreadStore::new(LocalThreadStoreConfig { codex_home:home.clone(),sqlite:sqlite.clone(),default_model_provider_id:PROVIDER.into() },Some(state.clone()));
        let pool=codex_state::open_thread_history_db(&sqlite).await.unwrap();
        let root=id(ROOT); let child=id(CHILD); let grand=id(GRAND);
        let key=references.keys().find(|p|p.starts_with(&format!("{name}/sessions/")) && p.ends_with("_33333333-3333-4333-8333-333333333333.jsonl")).unwrap();
        let mut imported: Vec<RolloutItem>=lines(&input.join(key)).into_iter().filter_map(|(l,_,_)| match l.item {
            RolloutItem::ResponseItem(r)=>Some(RolloutItem::ResponseItem(r)), _=>None }).collect();
        assert_eq!(imported.len(),4);
        // Golden grow is a Go-escaping size case, not a native serde size guarantee.
        // Preserve all four imported raw items and add a separate typed synthetic summary
        // with genuinely different content lengths; same record count in both phases.
        let long=(variant=="grow" && phase=="after") || (variant=="shrink" && phase=="before");
        let summary=format!("Context summary from synthetic native size exercise:\n{}", "s".repeat(if long {4096} else {8}));
        imported.push(raw_message(&summary));
        let expected_raw=raw_values(&imported);
        assert_eq!(expected_raw[1]["encrypted_content"],"OPAQUE_SYNTHETIC_NOT_REAL");
        assert_eq!(expected_raw[2]["call_id"],"call_synthetic");
        assert_eq!(expected_raw[3]["call_id"],expected_raw[2]["call_id"]);
        store.create_thread(create(root,&cwd,None,None)).await.unwrap();
        store.persist_thread(root,PersistContext::Standard).await.unwrap();
        append(&store,root,turn(root,"A-retained",imported,Some(&summary))).await;
        append(&store,root,turn(root,"A-discarded",vec![raw_message("DISCARDED_RAW_SENTINEL")],None)).await;
        let a=store.live_rollout_path(root).await.unwrap();
        seed(&state,root,&a).await;
        let a_stats=check_projection(&pool,&a).await;
        let summary_record_bytes=lines(&a).iter().find_map(|(line,start,end)| {
            let values=raw_values(std::slice::from_ref(&line.item));
            (values.first()==expected_raw.last()).then_some(end-start)
        }).unwrap();
        assert!(summary_record_bytes>=summary.len() as u64 && summary_record_bytes<=summary.len() as u64+1024);
        store.flush_thread(root).await.unwrap(); store.flush_thread(root).await.unwrap();
        assert_eq!(a_stats,check_projection(&pool,&a).await);
        store.shutdown_thread(root).await.unwrap(); let a_bytes=fs::read(&a).unwrap();
        store.revert_thread(RevertThreadParams { thread_id:root,before_turn_id:"A-discarded".into(),multi_agent_version:None }).await.unwrap();
        let b=state.get_thread(root).await.unwrap().unwrap().rollout_path;
        let b_id=codex_rollout::rollout_id_from_path(&b).unwrap(); assert_ne!(b_id,root); assert_ne!(a,b);
        let b_meta=codex_rollout::read_session_meta_line(&b).await.unwrap().meta;
        assert_eq!(b_meta.id,root); assert_eq!(b_meta.history_base.unwrap().thread_id,root);
        let discarded=lines(&a).into_iter().find(|(l,_,_)|matches!(&l.item,RolloutItem::EventMsg(EventMsg::TurnStarted(e)) if e.turn_id=="A-discarded")).unwrap();
        assert_eq!(b_meta.history_base.unwrap().end_ordinal_exclusive,discarded.0.ordinal.unwrap());
        assert_eq!(b_meta.history_base.unwrap().end_byte_offset,discarded.1);
        visible(&store,root,&["A-retained"]).await;
        let replay=store.resume_thread(ResumeThreadParams { thread_id:root,rollout_path:Some(b.clone()),
            history:None,history_revision:None,include_archived:false,metadata:metadata(&cwd) }).await.unwrap();
        let replay_raw=raw_values(&replay); assert!(retained_raw_matches(&replay_raw,&expected_raw));
        assert!(!serde_json::to_string(&replay_raw).unwrap().contains("DISCARDED_RAW_SENTINEL"));
        append(&store,root,turn(root,"B-new",vec![raw_message("B_NEW_RAW")],None)).await;
        let b_stats=check_projection(&pool,&b).await;
        let c=fork(&store,&state,&cwd,root,child,b_id).await;
        append(&store,child,turn(child,"C-new",vec![raw_message("C_NEW_RAW")],None)).await;
        let c_stats=check_projection(&pool,&c).await;
        let d=fork(&store,&state,&cwd,child,grand,child).await;
        append(&store,grand,turn(grand,"D-new",vec![raw_message("D_NEW_RAW")],None)).await;
        let d_stats=check_projection(&pool,&d).await;
        visible(&store,root,&["A-retained","B-new"]).await;
        visible(&store,child,&["A-retained","B-new","C-new"]).await;
        visible(&store,grand,&["A-retained","B-new","C-new","D-new"]).await;
        let context=store.load_latest_model_context(LoadThreadHistoryParams { thread_id:grand,include_archived:false }).await.unwrap();
        let raw=raw_values(&context.items); assert!(retained_raw_matches(&raw,&expected_raw));
        assert!(!serde_json::to_string(&raw).unwrap().contains("DISCARDED_RAW_SENTINEL"));
        for (kind,field) in [("reasoning","encrypted_content"),("function_call","call_id")] {
            let mut mutant=raw.clone(); let pos=mutant.iter().position(|v|v["type"]==kind).unwrap();
            mutant[pos][field]=json!("MUTATED_SYNTHETIC"); assert!(!retained_raw_matches(&mutant,&expected_raw));
        }
        // Seed repair must not override native revert's selected path with old immutable A.
        codex_rollout::state_db::reconcile_rollout(Some(&state),&a,PROVIDER,None,&[],Some(false),None).await;
        assert_eq!(state.get_thread(root).await.unwrap().unwrap().rollout_path,b);
        for (t,path,stats) in [(root,&b,&b_stats),(child,&c,&c_stats),(grand,&d,&d_stats)] {
            let unchanged=fs::read(path).unwrap();
            store.flush_thread(t).await.unwrap(); store.flush_thread(t).await.unwrap();
            assert_eq!(&check_projection(&pool,path).await,stats); assert_eq!(fs::read(path).unwrap(),unchanged);
            store.shutdown_thread(t).await.unwrap();
            assert_eq!(&check_projection(&pool,path).await,stats);
        }
        assert_eq!(fs::read(&a).unwrap(),a_bytes);
        let index=RolloutReferenceIndex::scan(&home).await.unwrap();
        assert_eq!(index.rollouts_for_thread(root).count(),2);
        assert_eq!(index.rollouts_for_thread(child).count(),1); assert_eq!(index.rollouts_for_thread(grand).count(),1);
        assert_eq!(index.history_base(child).unwrap().thread_id,b_id);
        assert_eq!(index.history_base(grand).unwrap().thread_id,child);
        let folder=output.join(&name); fs::create_dir_all(&folder).unwrap();
        for (role,path) in [("A",&a),("B",&b),("C",&c),("D",&d)] { fs::copy(path,folder.join(format!("{role}.jsonl"))).unwrap(); }
        report.insert(name,json!({"ids":{"A":root.to_string(),"B":b_id.to_string(),"C":child.to_string(),"D":grand.to_string()},
            "source_golden":key,"native_summary_utf8_bytes":summary.len(),"native_summary_record_bytes":summary_record_bytes,"A":a_stats,"B":b_stats,"C":c_stats,"D":d_stats}));
        pool.close().await; drop(store); state.close().await; drop(state); sandbox.close().unwrap();
    }}
    for variant in ["shrink","grow"] {
        let before=&report[&format!("{variant}/before")]; let after=&report[&format!("{variant}/after")];
        for role in ["A","B","C","D"] {
            assert_eq!(before[role]["ordinals"],after[role]["ordinals"]);
            assert_eq!(before[role]["next_ordinal"],after[role]["next_ordinal"]);
        }
        let summary_before=before["native_summary_record_bytes"].as_u64().unwrap();
        let summary_after=after["native_summary_record_bytes"].as_u64().unwrap();
        assert!(summary_before.abs_diff(summary_after)>=3072);
        assert_eq!(summary_after>summary_before,variant=="grow");
        let grew=after["A"]["next_byte_offset"].as_u64().unwrap()>before["A"]["next_byte_offset"].as_u64().unwrap();
        assert_eq!(grew,variant=="grow");
        assert_ne!(before["A"]["next_byte_offset"],after["A"]["next_byte_offset"]);
    }
    for (path,bytes) in originals { assert_eq!(fs::read(input.join(path)).unwrap(),bytes); }
    fs::write(output.join("manifest.json"),serde_json::to_vec_pretty(&report).unwrap()).unwrap();
    println!("REAL_LOCAL_PROJECTION_PASS cases=4 revisions_per_case=4 golden_unchanged=16 raw_mutants_per_case=2 wrong_reference_mutants_per_case=2 local_resume_only=1");
}
