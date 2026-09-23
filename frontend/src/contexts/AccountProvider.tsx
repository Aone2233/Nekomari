import React from "react";
import { AccountContext, type Account } from "./AccountContext";

const requestAccount = async (): Promise<Account> => {
    const response = await fetch("/api/me");
    if (!response.ok) {
        throw new Error("Failed to fetch account data");
    }
    return (await response.json()) as Account;
};

// Provider组件
export const AccountProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
    const [account, setAccount] = React.useState<Account | null>(null);
    const [loading, setLoading] = React.useState(true);
    const [error, setError] = React.useState<Error | null>(null);

    // 只负责发起请求并在响应到达后应用结果：函数体内没有同步的 setState，
    // 所以挂载 effect 可以直接调用它。refresh 保留原有的同步写入（loading/error），
    // 供外部调用方使用。
    const loadAccount = () =>
        requestAccount()
            .then((data) => {
                setAccount(data);
            })
            .catch((err) => {
                setError(err as Error);
            })
            .finally(() => {
                setLoading(false);
            });

    const refresh = async () => {
        setLoading(true);
        setError(null);
        await loadAccount();
    };

    React.useEffect(() => {
        // 挂载加载：refresh() 的同步写入（loading=true、error=null）与初始状态
        // 完全一致（loading 初值 true、error 初值 null），因此这里只发起同一次请求；
        // 所有状态写入都发生在响应之后。
        void loadAccount();
    }, []);

    return (
        <AccountContext.Provider value={{ account, loading, error, refresh }}>
        {children}
        </AccountContext.Provider>
    );
}
