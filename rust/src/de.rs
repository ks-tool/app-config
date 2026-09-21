//! serde over a flat set of variables: a struct is a path, a field one more segment
//! of it, a list the indices under it, and a value a string that says what it is.

use std::cell::RefCell;
use std::collections::{BTreeMap, BTreeSet};

use serde::de::{self, DeserializeSeed, IntoDeserializer, MapAccess, SeqAccess, Visitor};

use crate::{variable, Error};

type Seen<'a> = &'a RefCell<BTreeSet<String>>;

/// A place in the set: a path under which keys lie, or a value taken out of a list.
pub(crate) enum Node<'a> {
    Path {
        kv: &'a BTreeMap<String, String>,
        path: String,
        seen: Seen<'a>,
    },
    Value(String),
}

impl<'a> Node<'a> {
    pub(crate) fn root(kv: &'a BTreeMap<String, String>, seen: Seen<'a>) -> Self {
        Node::Path {
            kv,
            path: String::new(),
            seen,
        }
    }

    fn child(kv: &'a BTreeMap<String, String>, path: &str, segment: &str, seen: Seen<'a>) -> Self {
        let path = if path.is_empty() {
            segment.to_string()
        } else {
            format!("{path}_{segment}")
        };
        Node::Path { kv, path, seen }
    }

    /// Whether anything lies here: a value at the path itself, or keys under it.
    fn present(&self) -> bool {
        match self {
            Node::Value(_) => true,
            Node::Path { kv, path, .. } => {
                kv.contains_key(path) || under(kv, path).next().is_some()
            }
        }
    }

    /// The value here, marked as read.
    fn value(&self) -> Result<String, Error> {
        match self {
            Node::Value(v) => Ok(v.clone()),
            Node::Path { kv, path, seen } => match kv.get(path) {
                Some(v) => {
                    seen.borrow_mut().insert(path.clone());
                    Ok(v.clone())
                }
                None => Err(Error::Value(format!(
                    "{} is a section, and a value was wanted",
                    show(path)
                ))),
            },
        }
    }

    fn name(&self) -> String {
        match self {
            Node::Value(v) => format!("{v:?}"),
            Node::Path { path, .. } => show(path),
        }
    }

    fn parse<T: std::str::FromStr>(&self, what: &str) -> Result<T, Error> {
        let v = self.value()?;
        v.trim()
            .parse()
            .map_err(|_| Error::Value(format!("{}: {v:?} is no {what}", self.name())))
    }
}

fn show(path: &str) -> String {
    if path.is_empty() {
        "the configuration".to_string()
    } else {
        path.to_string()
    }
}

/// The keys strictly under `path`, with what follows it.
fn under<'k>(
    kv: &'k BTreeMap<String, String>,
    path: &str,
) -> impl Iterator<Item = (&'k str, &'k String)> {
    let prefix = if path.is_empty() {
        String::new()
    } else {
        format!("{path}_")
    };
    let root = path.is_empty();
    kv.iter().filter_map(move |(k, v)| {
        if root {
            Some((k.as_str(), v))
        } else {
            k.strip_prefix(&prefix).map(|rest| (rest, v))
        }
    })
}

macro_rules! number {
    ($($method:ident $visit:ident $ty:ty, $what:literal;)*) => {$(
        fn $method<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Error> {
            visitor.$visit(self.parse::<$ty>($what)?)
        }
    )*};
}

impl<'de, 'a> de::Deserializer<'de> for Node<'a> {
    type Error = Error;

    fn deserialize_any<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Error> {
        visitor.visit_string(self.value()?)
    }

    number! {
        deserialize_i8 visit_i8 i8, "whole number";
        deserialize_i16 visit_i16 i16, "whole number";
        deserialize_i32 visit_i32 i32, "whole number";
        deserialize_i64 visit_i64 i64, "whole number";
        deserialize_u8 visit_u8 u8, "whole number that is not below zero";
        deserialize_u16 visit_u16 u16, "whole number that is not below zero";
        deserialize_u32 visit_u32 u32, "whole number that is not below zero";
        deserialize_u64 visit_u64 u64, "whole number that is not below zero";
        deserialize_f32 visit_f32 f32, "number";
        deserialize_f64 visit_f64 f64, "number";
    }

    fn deserialize_bool<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Error> {
        let v = self.value()?;
        match v.trim().to_ascii_lowercase().as_str() {
            "true" | "1" | "yes" | "on" => visitor.visit_bool(true),
            "false" | "0" | "no" | "off" | "" => visitor.visit_bool(false),
            _ => Err(Error::Value(format!(
                "{}: {v:?} is neither true nor false",
                self.name()
            ))),
        }
    }

    fn deserialize_char<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Error> {
        visitor.visit_char(self.parse::<char>("single character")?)
    }

    fn deserialize_str<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Error> {
        visitor.visit_string(self.value()?)
    }

    fn deserialize_string<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Error> {
        visitor.visit_string(self.value()?)
    }

    fn deserialize_bytes<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Error> {
        visitor.visit_byte_buf(self.value()?.into_bytes())
    }

    fn deserialize_byte_buf<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Error> {
        visitor.visit_byte_buf(self.value()?.into_bytes())
    }

    fn deserialize_option<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Error> {
        if self.present() {
            visitor.visit_some(self)
        } else {
            visitor.visit_none()
        }
    }

    fn deserialize_unit<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Error> {
        visitor.visit_unit()
    }

    fn deserialize_unit_struct<V: Visitor<'de>>(
        self,
        _: &'static str,
        visitor: V,
    ) -> Result<V::Value, Error> {
        visitor.visit_unit()
    }

    fn deserialize_newtype_struct<V: Visitor<'de>>(
        self,
        _: &'static str,
        visitor: V,
    ) -> Result<V::Value, Error> {
        visitor.visit_newtype_struct(self)
    }

    /// A list is the values of one variable, by commas, or the indices under the path.
    fn deserialize_seq<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Error> {
        match &self {
            Node::Value(v) => visitor.visit_seq(Items::values(v)),
            Node::Path { kv, path, seen } => {
                if kv.contains_key(path) {
                    return visitor.visit_seq(Items::values(&self.value()?));
                }
                let mut indices = BTreeSet::new();
                for (rest, _) in under(kv, path) {
                    let head = rest.split('_').next().unwrap_or(rest);
                    match head.parse::<usize>() {
                        Ok(i) => {
                            indices.insert(i);
                        }
                        Err(_) => {
                            return Err(Error::Value(format!(
                                "{}: a list has indices under it, and {rest} is none",
                                show(path)
                            )));
                        }
                    }
                }
                let nodes = indices
                    .into_iter()
                    .map(|i| Node::child(kv, path, &i.to_string(), seen))
                    .collect();
                visitor.visit_seq(Items { nodes })
            }
        }
    }

    fn deserialize_tuple<V: Visitor<'de>>(self, _: usize, visitor: V) -> Result<V::Value, Error> {
        self.deserialize_seq(visitor)
    }

    fn deserialize_tuple_struct<V: Visitor<'de>>(
        self,
        _: &'static str,
        _: usize,
        visitor: V,
    ) -> Result<V::Value, Error> {
        self.deserialize_seq(visitor)
    }

    /// A map is what lies under the path, each key whole: `LABELS_TIER=web` is `tier`.
    fn deserialize_map<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Error> {
        match self {
            Node::Value(_) => Err(Error::Value(format!(
                "{}: a map cannot be one value",
                self.name()
            ))),
            Node::Path { kv, path, seen } => {
                let entries = under(kv, &path)
                    .map(|(rest, _)| {
                        (
                            rest.to_ascii_lowercase(),
                            Node::child(kv, &path, rest, seen),
                        )
                    })
                    .collect::<Vec<_>>();
                visitor.visit_map(Entries {
                    entries: entries.into_iter(),
                    value: None,
                })
            }
        }
    }

    /// A struct is its fields, each a segment of the path; one with nothing under it is
    /// left out, and serde says whether that is a default or a mistake.
    fn deserialize_struct<V: Visitor<'de>>(
        self,
        _: &'static str,
        fields: &'static [&'static str],
        visitor: V,
    ) -> Result<V::Value, Error> {
        match self {
            Node::Value(_) => Err(Error::Value(format!(
                "{}: a section cannot be one value",
                self.name()
            ))),
            Node::Path { kv, path, seen } => {
                if kv.contains_key(&path) && !path.is_empty() {
                    return Err(Error::Value(format!(
                        "{path} is a value, and a section was wanted"
                    )));
                }
                let entries = fields
                    .iter()
                    .map(|f| (f.to_string(), Node::child(kv, &path, &variable(f), seen)))
                    .filter(|(_, node)| node.present())
                    .collect::<Vec<_>>();
                visitor.visit_map(Entries {
                    entries: entries.into_iter(),
                    value: None,
                })
            }
        }
    }

    fn deserialize_enum<V: Visitor<'de>>(
        self,
        _: &'static str,
        _: &'static [&'static str],
        visitor: V,
    ) -> Result<V::Value, Error> {
        visitor.visit_enum(self.value()?.into_deserializer())
    }

    fn deserialize_identifier<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Error> {
        visitor.visit_string(self.value()?)
    }

    fn deserialize_ignored_any<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, Error> {
        visitor.visit_unit()
    }
}

struct Items<'a> {
    nodes: std::collections::VecDeque<Node<'a>>,
}

impl<'a> Items<'a> {
    fn values(joined: &str) -> Self {
        let nodes = if joined.trim().is_empty() {
            Default::default()
        } else {
            joined
                .split(',')
                .map(|v| Node::Value(v.trim().to_string()))
                .collect()
        };
        Items { nodes }
    }
}

impl<'de, 'a> SeqAccess<'de> for Items<'a> {
    type Error = Error;

    fn next_element_seed<T: DeserializeSeed<'de>>(
        &mut self,
        seed: T,
    ) -> Result<Option<T::Value>, Error> {
        self.nodes
            .pop_front()
            .map(|node| seed.deserialize(node))
            .transpose()
    }
}

struct Entries<'a, I: Iterator<Item = (String, Node<'a>)>> {
    entries: I,
    value: Option<Node<'a>>,
}

impl<'de, 'a, I: Iterator<Item = (String, Node<'a>)>> MapAccess<'de> for Entries<'a, I> {
    type Error = Error;

    fn next_key_seed<K: DeserializeSeed<'de>>(
        &mut self,
        seed: K,
    ) -> Result<Option<K::Value>, Error> {
        match self.entries.next() {
            Some((key, node)) => {
                self.value = Some(node);
                seed.deserialize(key.into_deserializer()).map(Some)
            }
            None => Ok(None),
        }
    }

    fn next_value_seed<T: DeserializeSeed<'de>>(&mut self, seed: T) -> Result<T::Value, Error> {
        seed.deserialize(
            self.value
                .take()
                .expect("a value is asked for after its key"),
        )
    }
}
