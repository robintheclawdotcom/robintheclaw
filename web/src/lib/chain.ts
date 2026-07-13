export const robinhoodMainnetChainId = 4_663;
export const robinhoodMainnetExplorer = "https://robinhoodchain.blockscout.com";
export const robinhoodAppChainId = 46_630;
export const robinhoodAppExplorer = "https://explorer.testnet.chain.robinhood.com";

export function mainnetTransactionUrl(hash: string) {
  return `${robinhoodMainnetExplorer}/tx/${hash}`;
}

export function appTransactionUrl(hash: string) {
  return `${robinhoodAppExplorer}/tx/${hash}`;
}
