export const robinhoodMainnetChainId = 4_663;
export const robinhoodMainnetExplorer = "https://robinhoodchain.blockscout.com";

export function mainnetTransactionUrl(hash: string) {
  return `${robinhoodMainnetExplorer}/tx/${hash}`;
}
