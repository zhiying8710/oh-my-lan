// 阻止 Windows release 构建时弹出额外 console。
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    oh_my_lan_desktop_lib::run()
}
