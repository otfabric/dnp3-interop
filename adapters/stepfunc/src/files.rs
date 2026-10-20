// SPDX-License-Identifier: MIT

//! File transfer, group 70, for the master (contract 1.1; AUTHENTICATE_FILE
//! and append, 1.3). The library has no outstation side and no DELETE_FILE.

use crate::util::*;

use dnp3::app::MaybeAsync;
use dnp3::app::{FileStatus, FileType, PermissionSet, Permissions};
use dnp3::master::*;
use serde_json::{json, Value};
use tokio::sync::oneshot;

pub const OPERATIONS: [&str; 5] = [
    "file-get",
    "file-put",
    "file-ls",
    "file-info",
    "file-authenticate",
];

pub enum FileOp {
    Get(String),
    Put(String, Vec<u8>, bool),
    List(String),
    Info(String),
    Authenticate,
}

/// A file operation and the credentials of --file-user and --file-password,
/// for AUTHENTICATE_FILE before the open (contract 1.3).
pub struct FileRequest {
    op: FileOp,
    credentials: Option<FileCredentials>,
}

pub fn parse(op: &str, args: &Args) -> Result<FileRequest, UsageError> {
    let credentials = match (args.has("file-user"), args.has("file-password")) {
        (true, true) => Some(FileCredentials {
            user_name: args.required("file-user")?,
            password: args.required("file-password")?,
        }),
        (false, false) if op != "file-authenticate" => None,
        (false, false) => return usage("file-authenticate needs --file-user and --file-password"),
        _ => return usage("--file-user and --file-password go together"),
    };
    if op == "file-authenticate" {
        return Ok(FileRequest {
            op: FileOp::Authenticate,
            credentials,
        });
    }
    let name = args.required("name")?;
    let valid = name == "/"
        || (name.starts_with('/')
            && name.len() <= 200
            && name[1..].split('/').all(|part| {
                !part.is_empty()
                    && !part.starts_with('.')
                    && part
                        .chars()
                        .all(|c| c.is_ascii_alphanumeric() || "._-".contains(c))
            }));
    if !valid {
        return usage("--name must be an absolute file name");
    }
    let op = match op {
        "file-get" => FileOp::Get(name),
        "file-put" => {
            let text = args.required("content")?;
            match from_hex(&text) {
                Some(data) => FileOp::Put(name, data, args.has("append")),
                None => return usage("--content must be hex"),
            }
        }
        "file-ls" => FileOp::List(name),
        _ => FileOp::Info(name),
    };
    Ok(FileRequest { op, credentials })
}

fn from_hex(text: &str) -> Option<Vec<u8>> {
    if !text.len().is_multiple_of(2) {
        return None;
    }
    (0..text.len())
        .step_by(2)
        .map(|i| u8::from_str_radix(text.get(i..i + 2)?, 16).ok())
        .collect()
}

pub fn status_name(s: FileStatus) -> &'static str {
    match s {
        FileStatus::Success => "SUCCESS",
        FileStatus::PermissionDenied => "PERMISSION_DENIED",
        FileStatus::InvalidMode => "INVALID_MODE",
        FileStatus::FileNotFound => "FILE_NOT_FOUND",
        FileStatus::FileLocked => "FILE_LOCKED",
        FileStatus::TooManyOpen => "TOO_MANY_OPEN",
        FileStatus::InvalidHandle => "INVALID_HANDLE",
        FileStatus::WriteBlockSize => "WRITE_BLOCK_SIZE",
        FileStatus::CommLost => "COMM_LOST",
        FileStatus::CannotAbort => "CANNOT_ABORT",
        FileStatus::NotOpened => "NOT_OPENED",
        FileStatus::HandleExpired => "HANDLE_EXPIRED",
        FileStatus::BufferOverrun => "BUFFER_OVERRUN",
        FileStatus::Fatal => "FATAL",
        FileStatus::BlockSeq => "BLOCK_SEQ",
        _ => "UNDEFINED",
    }
}

fn permissions(p: &Permissions) -> String {
    let set = |s: &PermissionSet| {
        format!(
            "{}{}{}",
            if s.read { 'r' } else { '-' },
            if s.write { 'w' } else { '-' },
            if s.execute { 'x' } else { '-' }
        )
    };
    format!("{}{}{}", set(&p.owner), set(&p.group), set(&p.world))
}

fn entry(info: &FileInfo) -> Value {
    json!({
        "name": info.name,
        "type": if matches!(info.file_type, FileType::Directory) { "directory" } else { "file" },
        "size": info.size,
        "permissions": permissions(&info.permissions),
        "time": format_time(info.time_created.raw_value()),
    })
}

/// What a file operation adds to the result document; the error code and
/// message when it failed.
#[derive(Default)]
pub struct FileResult {
    pub status: Option<&'static str>,
    pub file: Option<Value>,
    pub files: Option<Value>,
}

pub struct FileFailure {
    pub code: &'static str,
    pub message: String,
}

fn failure(e: FileError, result: &mut FileResult) -> FileFailure {
    match e {
        // AUTHENTICATE_FILE answered with key 0.
        FileError::NoPermission => {
            failure(FileError::BadStatus(FileStatus::PermissionDenied), result)
        }
        FileError::BadStatus(s) => {
            result.status = Some(status_name(s));
            FileFailure {
                code: "file-error",
                message: format!("the outstation answered {}", status_name(s)),
            }
        }
        FileError::TaskError(TaskError::ResponseTimeout) => FileFailure {
            code: "timeout",
            message: "no response from the outstation".into(),
        },
        FileError::TaskError(t @ TaskError::RejectedByIin2(_)) => FileFailure {
            code: "rejected",
            message: format!("the outstation refused the request: {t}"),
        },
        FileError::TaskError(
            t @ (TaskError::NoConnection
            | TaskError::Link(_)
            | TaskError::Transport
            | TaskError::Shutdown),
        ) => FileFailure {
            code: "connection-lost",
            message: format!("{t}"),
        },
        e => FileFailure {
            code: "failed",
            message: format!("{e}"),
        },
    }
}

/// Collects a file the library reads block by block.
struct Collector {
    data: Vec<u8>,
    done: Option<oneshot::Sender<Result<Vec<u8>, FileError>>>,
}

impl FileReader for Collector {
    fn opened(&mut self, _size: u32) -> FileAction {
        FileAction::Continue
    }

    fn block_received(&mut self, _block_num: u32, data: &[u8]) -> MaybeAsync<FileAction> {
        self.data.extend_from_slice(data);
        MaybeAsync::ready(FileAction::Continue)
    }

    fn aborted(&mut self, err: FileError) {
        if let Some(tx) = self.done.take() {
            let _ = tx.send(Err(err));
        }
    }

    fn completed(&mut self) {
        if let Some(tx) = self.done.take() {
            let _ = tx.send(Ok(std::mem::take(&mut self.data)));
        }
    }
}

pub async fn run(
    association: &mut AssociationHandle,
    request: FileRequest,
    result: &mut FileResult,
) -> Option<FileFailure> {
    let FileRequest { op, credentials } = request;
    let outcome: Result<(), FileError> = async {
        match op {
            FileOp::Authenticate => {
                if let Some(c) = credentials {
                    association.get_file_auth_key(c).await?;
                }
            }
            FileOp::Get(name) => {
                let (tx, rx) = oneshot::channel();
                let reader = Box::new(Collector {
                    data: Vec::new(),
                    done: Some(tx),
                });
                association
                    .read_file(name.clone(), FileReadConfig::default(), reader, credentials)
                    .await
                    .map_err(|_| FileError::TaskError(TaskError::Shutdown))?;
                let data = rx
                    .await
                    .map_err(|_| FileError::TaskError(TaskError::Shutdown))??;
                result.file =
                    Some(json!({"name": name, "size": data.len(), "content": to_hex(&data)}));
            }
            FileOp::Put(name, data, append) => {
                let key = match credentials {
                    Some(c) => association.get_file_auth_key(c).await?,
                    None => AuthKey::none(),
                };
                let open = association
                    .open_file(
                        name,
                        key,
                        Permissions::default(),
                        data.len() as u32,
                        if append {
                            FileMode::Append
                        } else {
                            FileMode::Write
                        },
                        u16::MAX,
                    )
                    .await?;
                let size = usize::from(open.max_block_size.max(1));
                let mut block = BlockNumber::default();
                let chunks: Vec<&[u8]> = if data.is_empty() {
                    vec![&[]]
                } else {
                    data.chunks(size).collect()
                };
                for (i, chunk) in chunks.iter().enumerate() {
                    if i + 1 == chunks.len() {
                        block.set_last();
                    }
                    association
                        .write_file_block(open.file_handle, block, chunk.to_vec())
                        .await?;
                    let _ = block.increment();
                }
                association.close_file(open.file_handle).await?;
            }
            FileOp::List(name) => {
                let entries = association
                    .read_directory(name, DirReadConfig::default(), credentials)
                    .await?;
                result.files = Some(Value::Array(entries.iter().map(entry).collect()));
            }
            FileOp::Info(name) => {
                let info = association.get_file_info(name).await?;
                result.file = Some(entry(&info));
            }
        }
        Ok(())
    }
    .await;
    match outcome {
        Ok(()) => {
            result.status = Some("SUCCESS");
            None
        }
        Err(e) => Some(failure(e, result)),
    }
}
